package server

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func signJWT(t *testing.T, key crypto.Signer, kid string, claims map[string]any) string {
	t.Helper()
	alg := "RS256"
	if _, ok := key.(*ecdsa.PrivateKey); ok {
		alg = "ES256"
	}
	h, _ := json.Marshal(map[string]string{"alg": alg, "kid": kid, "typ": "JWT"})
	c, _ := json.Marshal(claims)
	input := b64(h) + "." + b64(c)
	digest := sha256.Sum256([]byte(input))
	var sig []byte
	switch k := key.(type) {
	case *rsa.PrivateKey:
		sig, _ = rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, digest[:])
	case *ecdsa.PrivateKey:
		r, s, _ := ecdsa.Sign(rand.Reader, k, digest[:])
		sig = make([]byte, 64)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:])
	}
	return input + "." + b64(sig)
}

// fakeIdP is a minimal OpenID provider: discovery, JWKS, and a token
// endpoint that checks PKCE and returns whatever ID token claims were queued.
type fakeIdP struct {
	srv   *httptest.Server
	rsa   *rsa.PrivateKey
	ec    *ecdsa.PrivateKey
	mu    sync.Mutex
	codes map[string]idpCode
}

type idpCode struct {
	challenge string
	claims    map[string]any
	key       crypto.Signer
}

func newFakeIdP(t *testing.T) *fakeIdP {
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	ek, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	p := &fakeIdP{rsa: rk, ec: ek, codes: map[string]idpCode{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 p.srv.URL,
			"authorization_endpoint": p.srv.URL + "/authorize",
			"token_endpoint":         p.srv.URL + "/token",
			"jwks_uri":               p.srv.URL + "/jwks",
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{
			{"kty": "RSA", "kid": "r1", "use": "sig", "n": b64(rk.N.Bytes()), "e": b64(big.NewInt(int64(rk.E)).Bytes())},
			{"kty": "EC", "kid": "e1", "crv": "P-256", "x": b64(ek.X.FillBytes(make([]byte, 32))), "y": b64(ek.Y.FillBytes(make([]byte, 32)))},
		}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		p.mu.Lock()
		c, ok := p.codes[r.Form.Get("code")]
		delete(p.codes, r.Form.Get("code"))
		p.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || b64(sum[:]) != c.challenge || r.Form.Get("client_secret") != "shh" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		kid := "r1"
		if _, isEC := c.key.(*ecdsa.PrivateKey); isEC {
			kid = "e1"
		}
		json.NewEncoder(w).Encode(map[string]string{"id_token": signJWT(t, c.key, kid, c.claims)})
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func enableOIDC(e *testEnv, idp *fakeIdP, domains ...string) {
	e.s.cfg.PublicURL = e.srv.URL
	e.s.cfg.OIDCIssuer = idp.srv.URL
	e.s.cfg.OIDCClientID = "workshell"
	e.s.cfg.OIDCClientSecret = "shh"
	e.s.cfg.OIDCName = "Acme"
	e.s.cfg.SSOAllowedDomains = domains
	e.s.initSSO()
}

// oidcSignIn runs the browser side of the flow. edit can change the ID token
// claims the provider will issue; key overrides the signing key.
func oidcSignIn(e *testEnv, idp *fakeIdP, client *http.Client, claims map[string]any, key crypto.Signer) (status int, location string) {
	e.t.Helper()
	noFollow := *client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := noFollow.Get(e.srv.URL + "/api/auth/oidc/start?next=/p/1/issues")
	if err != nil {
		e.t.Fatal(err)
	}
	res.Body.Close()
	auth, _ := url.Parse(res.Header.Get("Location"))
	q := auth.Query()
	if !strings.HasPrefix(auth.String(), idp.srv.URL+"/authorize") || q.Get("code_challenge_method") != "S256" {
		e.t.Fatalf("start redirected to %s", auth)
	}
	full := map[string]any{
		"iss": idp.srv.URL, "aud": "workshell", "sub": "user-1", "nonce": q.Get("nonce"),
		"email": "sam@acme.dev", "email_verified": true, "name": "Sam",
		"iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(),
	}
	for k, v := range claims {
		if v == nil {
			delete(full, k)
		} else {
			full[k] = v
		}
	}
	if key == nil {
		key = idp.rsa
	}
	code := randomHex(8)
	idp.mu.Lock()
	idp.codes[code] = idpCode{challenge: q.Get("code_challenge"), claims: full, key: key}
	idp.mu.Unlock()
	res, err = noFollow.Get(e.srv.URL + "/api/auth/oidc/callback?" + url.Values{"code": {code}, "state": {q.Get("state")}}.Encode())
	if err != nil {
		e.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode, res.Header.Get("Location")
}

func newBrowser() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func whoami(e *testEnv, c *http.Client, header string) (email, ssoError string) {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/auth/state", nil)
	if header != "" {
		req.Header.Set(cfAccessHeader, header)
	}
	res, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	var st struct {
		User     *user
		SSOError string `json:"ssoError"`
	}
	json.NewDecoder(res.Body).Decode(&st)
	if st.User != nil {
		email = st.User.Email
	}
	return email, st.SSOError
}

func TestOIDCSignIn(t *testing.T) {
	e := newTestEnv(t)
	idp := newFakeIdP(t)
	enableOIDC(e, idp, "acme.dev")

	// Before the admin account exists, SSO can't create anything.
	b := newBrowser()
	if _, loc := oidcSignIn(e, idp, b, nil, nil); !strings.Contains(loc, "error=") {
		t.Fatalf("SSO before setup should fail, went to %s", loc)
	}
	e.setup()

	// An allowed domain creates an account and returns to where they started.
	b = newBrowser()
	if code, loc := oidcSignIn(e, idp, b, nil, nil); code != http.StatusSeeOther || loc != "/p/1/issues" {
		t.Fatalf("sign-in: %d %s", code, loc)
	}
	if email, _ := whoami(e, b, ""); email != "sam@acme.dev" {
		t.Fatalf("signed in as %q", email)
	}
	// Signing in again maps to the same account through the identity.
	b2 := newBrowser()
	oidcSignIn(e, idp, b2, map[string]any{"email": "sam.renamed@acme.dev"}, nil)
	if email, _ := whoami(e, b2, ""); email != "sam@acme.dev" {
		t.Fatalf("second sign-in landed on %q", email)
	}
	// The SSO-only account has no password to guess.
	if code := e.do("POST", "/api/auth/logout", map[string]any{}, nil); code != 200 {
		t.Fatal(code)
	}
	if code := e.do("POST", "/api/auth/login", map[string]string{"email": "sam@acme.dev", "password": ""}, nil); code != http.StatusUnauthorized {
		t.Fatalf("empty password login: %d", code)
	}

	rejected := []struct {
		name   string
		claims map[string]any
		key    crypto.Signer
	}{
		{"other domain", map[string]any{"sub": "u2", "email": "eve@evil.dev"}, nil},
		{"unverified email", map[string]any{"sub": "u3", "email": "pat@acme.dev", "email_verified": false}, nil},
		{"wrong audience", map[string]any{"sub": "u4", "email": "kim@acme.dev", "aud": "someone-else"}, nil},
		{"wrong issuer", map[string]any{"sub": "u5", "email": "kim@acme.dev", "iss": "https://evil.dev"}, nil},
		{"wrong nonce", map[string]any{"sub": "u6", "email": "kim@acme.dev", "nonce": "replayed"}, nil},
		{"expired", map[string]any{"sub": "u7", "email": "kim@acme.dev", "exp": time.Now().Add(-time.Hour).Unix()}, nil},
		{"foreign key", map[string]any{"sub": "u8", "email": "kim@acme.dev"}, mustRSA()},
	}
	for _, tc := range rejected {
		b := newBrowser()
		if _, loc := oidcSignIn(e, idp, b, tc.claims, tc.key); !strings.HasPrefix(loc, "/login?error=") {
			t.Errorf("%s: went to %s", tc.name, loc)
		}
		if email, _ := whoami(e, b, ""); email != "" {
			t.Errorf("%s: signed in as %s", tc.name, email)
		}
	}

	// ES256 tokens verify too, and an admin-added member links by email.
	// (The callbacks above count against the sign-in rate limit; start fresh.)
	e.s.limits.login = newLimiter()
	if code := e.do("POST", "/api/auth/login", map[string]string{"email": "admin@example.com", "password": "hunter2hunter2"}, nil); code != 200 {
		t.Fatalf("admin login: %d", code)
	}
	if code := e.do("POST", "/api/users", map[string]any{"name": "Kim", "email": "kim@partner.io"}, nil); code != 200 {
		t.Fatalf("add member without password: %d", code)
	}
	b = newBrowser()
	if code, _ := oidcSignIn(e, idp, b, map[string]any{"sub": "u9", "email": "kim@partner.io"}, idp.ec); code != http.StatusSeeOther {
		t.Fatalf("invited member: %d", code)
	}
	if email, _ := whoami(e, b, ""); email != "kim@partner.io" {
		t.Fatalf("invited member signed in as %q", email)
	}

	// A callback without the browser's state cookie is refused.
	b = newBrowser()
	res, _ := b.Get(e.srv.URL + "/api/auth/oidc/callback?code=x&state=y")
	res.Body.Close()
	if email, _ := whoami(e, b, ""); email != "" {
		t.Fatal("callback without state signed someone in")
	}
}

func mustRSA() *rsa.PrivateKey { k, _ := rsa.GenerateKey(rand.Reader, 2048); return k }

func TestCloudflareAccess(t *testing.T) {
	e := newTestEnv(t)
	idp := newFakeIdP(t)
	e.setup()
	e.do("POST", "/api/auth/logout", map[string]any{}, nil)
	issuer := "https://acme.cloudflareaccess.com"
	e.s.cfAccess = &cfAccess{issuer: issuer, aud: "aud-tag", keys: newJWKS(idp.srv.URL + "/jwks")}
	token := func(claims map[string]any, key crypto.Signer) string {
		full := map[string]any{"iss": issuer, "aud": []string{"aud-tag"}, "sub": "cf-1", "email": "admin@example.com",
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()}
		for k, v := range claims {
			full[k] = v
		}
		return signJWT(t, key, "r1", full)
	}

	// A valid Access token signs the matching account straight in.
	b := newBrowser()
	if email, _ := whoami(e, b, token(nil, idp.rsa)); email != "admin@example.com" {
		t.Fatalf("access sign-in: %q", email)
	}
	// After signing out, Access doesn't sign them back in until they ask.
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/auth/logout", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	res, _ := b.Do(req)
	res.Body.Close()
	if email, _ := whoami(e, b, token(nil, idp.rsa)); email != "" {
		t.Fatalf("signed back in after sign-out as %q", email)
	}
	req, _ = http.NewRequest("POST", e.srv.URL+"/api/auth/cf-access", nil)
	req.Header.Set(cfAccessHeader, token(nil, idp.rsa))
	if res, _ = b.Do(req); res.StatusCode != 200 {
		t.Fatalf("explicit access sign-in: %d", res.StatusCode)
	}
	res.Body.Close()
	if email, _ := whoami(e, b, ""); email != "admin@example.com" {
		t.Fatalf("after explicit sign-in: %q", email)
	}

	// Forged, foreign or mis-addressed tokens get nothing.
	for name, tok := range map[string]string{
		"forged":       token(nil, mustRSA()),
		"wrong aud":    token(map[string]any{"aud": "other"}, idp.rsa),
		"wrong issuer": token(map[string]any{"iss": "https://evil.cloudflareaccess.com"}, idp.rsa),
		"no email":     token(map[string]any{"email": ""}, idp.rsa),
		"garbage":      "a.b.c",
	} {
		email, msg := whoami(e, newBrowser(), tok)
		if email != "" || msg == "" {
			t.Errorf("%s: email=%q msg=%q", name, email, msg)
		}
	}
	// Unknown emails need an invite or an allowed domain.
	if email, msg := whoami(e, newBrowser(), token(map[string]any{"sub": "cf-2", "email": "new@else.dev"}, idp.rsa)); email != "" || !strings.Contains(msg, "Ask an admin") {
		t.Fatalf("unknown email: %q %q", email, msg)
	}
}

func TestSSOPasswordOptions(t *testing.T) {
	e := newTestEnv(t)
	idp := newFakeIdP(t)
	enableOIDC(e, idp, "acme.dev")
	e.setup()

	// An SSO-made account can set a first password without a current one.
	b := newBrowser()
	oidcSignIn(e, idp, b, nil, nil)
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/auth/password", strings.NewReader(`{"currentPassword":"","newPassword":"correct-horse"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := b.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("set first password: %v %d", err, res.StatusCode)
	}
	res.Body.Close()
	e.do("POST", "/api/auth/logout", map[string]any{}, nil)
	if code := e.do("POST", "/api/auth/login", map[string]string{"email": "sam@acme.dev", "password": "correct-horse"}, nil); code != 200 {
		t.Fatalf("login with new password: %d", code)
	}

	// With password sign-in off, only SSO works.
	e.s.cfg.NoPasswordLogin = true
	if code := e.do("POST", "/api/auth/login", map[string]string{"email": "admin@example.com", "password": "hunter2hunter2"}, nil); code != http.StatusForbidden {
		t.Fatalf("password login while disabled: %d", code)
	}
	if code, _ := oidcSignIn(e, idp, newBrowser(), nil, nil); code != http.StatusSeeOther {
		t.Fatalf("SSO while passwords are off: %d", code)
	}
}
