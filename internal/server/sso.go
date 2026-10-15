package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Single sign-on: an OpenID Connect provider (authorization code flow with
// PKCE), and Cloudflare Access, whose signed header identifies a user who
// already passed Access in front of Workshell.
//
// SSO never creates the first account. A verified email signs in to the
// account with that email, or creates one when its domain is allowed.

const (
	oidcStateCookie = "workshell_oidc"
	signedOutCookie = "workshell_signed_out"
	cfAccessHeader  = "Cf-Access-Jwt-Assertion"
)

type oidcFlow struct {
	verifier, nonce, next string
	expires               time.Time
}

type oidcProvider struct {
	issuer, clientID, clientSecret, name, redirectURL string

	mu        sync.Mutex
	authURL   string
	tokenURL  string
	keys      *jwks
	flows     map[string]oidcFlow
	discovery time.Time
}

type cfAccess struct {
	issuer, aud string
	keys        *jwks
}

func (s *Server) initSSO() {
	c := s.cfg
	if c.OIDCIssuer != "" && c.OIDCClientID != "" {
		if c.PublicURL == "" {
			s.log.Error("OIDC sign-in needs WORKSHELL_PUBLIC_URL for its redirect URL; OIDC is off")
		} else {
			s.oidc = &oidcProvider{
				issuer:       strings.TrimSuffix(c.OIDCIssuer, "/"),
				clientID:     c.OIDCClientID,
				clientSecret: c.OIDCClientSecret,
				name:         c.OIDCName,
				redirectURL:  strings.TrimSuffix(c.PublicURL, "/") + "/api/auth/oidc/callback",
				flows:        map[string]oidcFlow{},
			}
		}
	}
	if c.CFAccessTeam != "" && c.CFAccessAUD != "" {
		team := strings.TrimSuffix(strings.TrimPrefix(c.CFAccessTeam, "https://"), "/")
		if !strings.Contains(team, ".") {
			team += ".cloudflareaccess.com"
		}
		issuer := "https://" + team
		s.cfAccess = &cfAccess{issuer: issuer, aud: c.CFAccessAUD, keys: newJWKS(issuer + "/cdn-cgi/access/certs")}
	}
}

// ssoState is what the sign-in page needs to know.
func (s *Server) ssoState() map[string]any {
	var oidc any
	if s.oidc != nil {
		oidc = map[string]any{"name": s.oidc.name}
	}
	return map[string]any{
		"oidc":          oidc,
		"cfAccess":      s.cfAccess != nil,
		"passwordLogin": !s.cfg.NoPasswordLogin,
	}
}

func (p *oidcProvider) discover(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.keys != nil {
		return nil
	}
	if time.Since(p.discovery) < 10*time.Second {
		return errors.New("the sign-in provider is unavailable")
	}
	p.discovery = time.Now()
	var doc struct {
		Issuer   string `json:"issuer"`
		AuthURL  string `json:"authorization_endpoint"`
		TokenURL string `json:"token_endpoint"`
		JWKSURL  string `json:"jwks_uri"`
	}
	if err := fetchJSON(ctx, p.issuer+"/.well-known/openid-configuration", &doc); err != nil {
		return err
	}
	if strings.TrimSuffix(doc.Issuer, "/") != p.issuer {
		return fmt.Errorf("provider reports issuer %q, configured %q", doc.Issuer, p.issuer)
	}
	for _, u := range []string{doc.AuthURL, doc.TokenURL, doc.JWKSURL} {
		if pu, err := url.Parse(u); err != nil || pu.Scheme != "https" && pu.Hostname() != "localhost" && pu.Hostname() != "127.0.0.1" {
			return fmt.Errorf("provider endpoint %q is not https", u)
		}
	}
	// Keep the issuer exactly as the provider spells it; ID tokens use that.
	p.issuer = doc.Issuer
	p.authURL, p.tokenURL, p.keys = doc.AuthURL, doc.TokenURL, newJWKS(doc.JWKSURL)
	return nil
}

func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") || strings.HasPrefix(next, "/api/") {
		return "/"
	}
	return next
}

// loginRedirect sends the browser back to the sign-in page with a message.
func loginRedirect(w http.ResponseWriter, r *http.Request, msg string) error {
	http.Redirect(w, r, "/login?error="+url.QueryEscape(msg), http.StatusSeeOther)
	return nil
}

func (s *Server) handleOIDCStart(w http.ResponseWriter, r *http.Request) error {
	p := s.oidc
	if p == nil {
		return errNotFound
	}
	if err := p.discover(r.Context()); err != nil {
		s.log.Warn("oidc discovery", "err", err)
		return loginRedirect(w, r, "The sign-in provider couldn't be reached. Try again in a moment.")
	}
	state, verifier, nonce := randomHex(24), randomHex(32), randomHex(16)
	challenge := sha256.Sum256([]byte(verifier))
	p.mu.Lock()
	now := time.Now()
	for k, f := range p.flows {
		if now.After(f.expires) {
			delete(p.flows, k)
		}
	}
	if len(p.flows) > 10000 {
		p.mu.Unlock()
		return errStatus(http.StatusTooManyRequests, "too many sign-ins in progress")
	}
	p.flows[state] = oidcFlow{verifier: verifier, nonce: nonce, next: safeNext(r.URL.Query().Get("next")), expires: now.Add(10 * time.Minute)}
	authURL := p.authURL
	p.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookie, Value: state, Path: "/api/auth/oidc/", MaxAge: 600,
		HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode,
	})
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {p.clientID},
		"redirect_uri":          {p.redirectURL},
		"scope":                 {"openid email profile"},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(authURL, "?") {
		sep = "&"
	}
	http.Redirect(w, r, authURL+sep+q.Encode(), http.StatusFound)
	return nil
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) error {
	p := s.oidc
	if p == nil {
		return errNotFound
	}
	http.SetCookie(w, &http.Cookie{Name: oidcStateCookie, Value: "", Path: "/api/auth/oidc/", MaxAge: -1, HttpOnly: true})
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		return loginRedirect(w, r, "Sign-in was cancelled or refused by the provider.")
	}
	state := q.Get("state")
	c, err := r.Cookie(oidcStateCookie)
	if err != nil || state == "" || c.Value != state {
		return loginRedirect(w, r, "That sign-in link expired. Try again.")
	}
	p.mu.Lock()
	flow, found := p.flows[state]
	delete(p.flows, state)
	p.mu.Unlock()
	if !found || time.Now().After(flow.expires) {
		return loginRedirect(w, r, "That sign-in link expired. Try again.")
	}
	if !s.limits.login.take(clientIP(r, s.cfg.TrustProxy), 1, 10, 1.0/30) {
		return loginRedirect(w, r, "Too many sign-in attempts. Wait a minute and try again.")
	}

	claims, err := p.exchange(r.Context(), q.Get("code"), flow.verifier)
	if err != nil {
		s.log.Warn("oidc sign-in", "err", err)
		return loginRedirect(w, r, "Sign-in failed. The provider's response couldn't be verified.")
	}
	if claims.str("nonce") != flow.nonce {
		return loginRedirect(w, r, "Sign-in failed. The provider's response couldn't be verified.")
	}
	email := claims.str("email")
	verified, _ := claims["email_verified"].(bool)
	if email == "" || !verified && !s.cfg.OIDCTrustEmail {
		return loginRedirect(w, r, "Your sign-in provider didn't share a verified email address.")
	}
	id, err := s.ssoUser(r, "oidc:"+p.issuer, claims.str("sub"), email, claims.str("name"))
	if err != nil {
		return loginRedirect(w, r, ssoMessage(err))
	}
	if err := s.startSession(w, r, id); err != nil {
		return err
	}
	clearSignedOut(w)
	http.Redirect(w, r, flow.next, http.StatusSeeOther)
	return nil
}

// exchange trades the authorization code for tokens and verifies the ID token.
func (p *oidcProvider) exchange(ctx context.Context, code, verifier string) (jwtClaims, error) {
	if code == "" {
		return nil, errors.New("no code")
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {p.redirectURL},
		"code_verifier": {verifier},
		"client_id":     {p.clientID},
	}
	if p.clientSecret != "" {
		form.Set("client_secret", p.clientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := ssoHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var tok struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&tok); err != nil {
		return nil, fmt.Errorf("token response: %s", res.Status)
	}
	if res.StatusCode != http.StatusOK || tok.IDToken == "" {
		return nil, fmt.Errorf("token exchange: %s %s", res.Status, tok.Error)
	}
	claims, err := verifyJWT(ctx, tok.IDToken, p.keys, p.issuer, p.clientID)
	if err != nil {
		return nil, err
	}
	// With several audiences, the token must have been issued to us.
	if azp := claims.str("azp"); azp != "" && azp != p.clientID {
		return nil, errors.New("token issued to another client")
	}
	return claims, nil
}

// cfAccessUser verifies the Cloudflare Access header and finds the user.
// It returns 0 with no error when the request carries no Access token.
func (s *Server) cfAccessUser(r *http.Request) (int64, error) {
	a := s.cfAccess
	token := r.Header.Get(cfAccessHeader)
	if a == nil || token == "" {
		return 0, nil
	}
	claims, err := verifyJWT(r.Context(), token, a.keys, a.issuer, a.aud)
	if err != nil {
		s.log.Warn("cloudflare access token rejected", "err", err)
		return 0, errSSO("Your Cloudflare Access sign-in couldn't be verified.")
	}
	email := claims.str("email")
	if email == "" {
		return 0, errSSO("Cloudflare Access didn't include an email address (service tokens can't sign in).")
	}
	return s.ssoUser(r, "cf-access:"+a.issuer, claims.str("sub"), email, "")
}

// handleCFAccessLogin signs in with the Access token on this request, for the
// sign-in page's button (after an explicit sign-out).
func (s *Server) handleCFAccessLogin(w http.ResponseWriter, r *http.Request) error {
	if s.cfAccess == nil {
		return errNotFound
	}
	if r.Header.Get(cfAccessHeader) == "" {
		return errStatus(http.StatusUnauthorized, "this request didn't come through Cloudflare Access")
	}
	id, err := s.cfAccessUser(r)
	if err != nil {
		return errStatus(http.StatusForbidden, "%s", ssoMessage(err))
	}
	if err := s.startSession(w, r, id); err != nil {
		return err
	}
	clearSignedOut(w)
	return ok(w, map[string]any{"ok": true})
}

func clearSignedOut(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: signedOutCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
}

type errSSO string

func (e errSSO) Error() string { return string(e) }

func ssoMessage(err error) string {
	var m errSSO
	if errors.As(err, &m) {
		return string(m)
	}
	return "Sign-in failed. Try again."
}

// ssoUser finds or creates the user for a verified SSO identity.
func (s *Server) ssoUser(r *http.Request, provider, subject, email, name string) (int64, error) {
	ctx := r.Context()
	email = strings.TrimSpace(email)
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || subject == "" {
		return 0, errSSO("Your sign-in provider sent an invalid identity.")
	}
	now := nowMs()

	var id int64
	err = s.db.R.QueryRowContext(ctx, `SELECT user_id FROM user_identities WHERE provider = ? AND subject = ?`, provider, subject).Scan(&id)
	if err == nil {
		s.db.W.ExecContext(ctx, `UPDATE user_identities SET last_used = ?, email = ? WHERE provider = ? AND subject = ?`, now, email, provider, subject)
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}

	n, err := s.userCount(r)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, errSSO("Create the admin account first, then sign in with SSO.")
	}

	err = s.db.R.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ?`, email).Scan(&id)
	switch {
	case err == nil:
		// An account an admin added for this email: link it.
	case errors.Is(err, sql.ErrNoRows):
		if !s.domainAllowed(email) {
			return 0, errSSO(fmt.Sprintf("There's no Workshell account for %s. Ask an admin to add you.", email))
		}
		if name = strings.TrimSpace(name); name == "" || len(name) > 100 {
			name, _, _ = strings.Cut(email, "@")
		}
		handle, err := s.uniqueHandle(r, email)
		if err != nil {
			return 0, err
		}
		res, err := s.db.W.ExecContext(ctx,
			`INSERT INTO users (email, name, handle, password_hash, is_admin, created_at) VALUES (?, ?, ?, '', 0, ?)`,
			email, name, handle, now)
		if err != nil {
			return 0, err
		}
		if id, err = res.LastInsertId(); err != nil {
			return 0, err
		}
		s.audit(r, 0, &user{ID: id}, "user.sso_join", provider)
	default:
		return 0, err
	}
	_, err = s.db.W.ExecContext(ctx,
		`INSERT INTO user_identities (provider, subject, user_id, email, created_at, last_used) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (provider, subject) DO NOTHING`, provider, subject, id, email, now, now)
	return id, err
}

func (s *Server) domainAllowed(email string) bool {
	_, domain, _ := strings.Cut(strings.ToLower(email), "@")
	for _, d := range s.cfg.SSOAllowedDomains {
		if domain == strings.TrimPrefix(d, "@") {
			return true
		}
	}
	return false
}

func (s *Server) ssoEnabled() bool { return s.oidc != nil || s.cfAccess != nil }
