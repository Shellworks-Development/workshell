package server

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionCookie = "workshell_session"

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("workshell"), bcrypt.DefaultCost)

type user struct {
	ID        int64           `json:"id"`
	Email     string          `json:"email"`
	Name      string          `json:"name"`
	Handle    string          `json:"handle"`
	Title     string          `json:"title"`
	Avatar    string          `json:"avatar"`
	IsAdmin   bool            `json:"isAdmin"`
	CreatedAt int64           `json:"createdAt"`
	Prefs     json.RawMessage `json:"prefs,omitempty"`
}

const userColumns = `u.id, u.email, u.name, u.handle, u.title, u.avatar, u.is_admin, u.created_at, u.prefs`

func scanUser(row interface{ Scan(...any) error }) (*user, error) {
	var u user
	var prefs string
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Handle, &u.Title, &u.Avatar, &u.IsAdmin, &u.CreatedAt, &prefs); err != nil {
		return nil, err
	}
	u.Prefs = json.RawMessage(prefs)
	return &u, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Server) currentUser(r *http.Request) (*user, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, nil
	}
	u, err := scanUser(s.db.R.QueryRowContext(r.Context(), `
		SELECT `+userColumns+`
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ?`,
		hashToken(c.Value), nowMs()))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

func (s *Server) requireUser(fn func(w http.ResponseWriter, r *http.Request, u *user) error) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		u, err := s.currentUser(r)
		if err != nil {
			return err
		}
		if u == nil {
			return errUnauthorized
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			// Reject cross-site requests outright; SameSite cookies plus the JSON
			// content-type requirement already cover this, it's belt and braces.
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				return errForbidden
			}
		}
		return fn(w, r, u)
	}
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	token := randomHex(32)
	now := time.Now()
	expires := now.Add(s.cfg.SessionTTL)
	_, err := s.db.W.ExecContext(r.Context(),
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		hashToken(token), userID, now.UnixMilli(), expires.UnixMilli())
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (s *Server) userCount(r *http.Request) (int, error) {
	var n int
	err := s.db.R.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Server) handleAuthState(w http.ResponseWriter, r *http.Request) error {
	n, err := s.userCount(r)
	if err != nil {
		return err
	}
	u, err := s.currentUser(r)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{
		"setupRequired": n == 0,
		"user":          u,
		"publicUrl":     s.cfg.PublicURL,
	})
}

type credentials struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

func (c *credentials) validate(needName bool) error {
	c.Email = strings.TrimSpace(c.Email)
	c.Name = strings.TrimSpace(c.Name)
	if _, err := mail.ParseAddress(c.Email); err != nil {
		return badRequest("enter a valid email address")
	}
	if needName && c.Name == "" {
		return badRequest("name is required")
	}
	if len(c.Password) < 8 {
		return badRequest("password must be at least 8 characters")
	}
	if len(c.Password) > 72 {
		return badRequest("password must be at most 72 characters")
	}
	return nil
}

func (s *Server) createUser(r *http.Request, c credentials, admin bool) (int64, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(c.Password), bcrypt.DefaultCost)
	if err != nil {
		return 0, err
	}
	handle, err := s.uniqueHandle(r, c.Email)
	if err != nil {
		return 0, err
	}
	res, err := s.db.W.ExecContext(r.Context(),
		`INSERT INTO users (email, name, handle, password_hash, is_admin, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		c.Email, c.Name, handle, string(hash), admin, nowMs())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, errStatus(http.StatusConflict, "a user with that email already exists")
		}
		return 0, err
	}
	return res.LastInsertId()
}

// handleSetup creates the first (admin) account. It only works while no users exist.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) error {
	var c credentials
	if err := readJSON(r, &c); err != nil {
		return err
	}
	if err := c.validate(true); err != nil {
		return err
	}
	n, err := s.userCount(r)
	if err != nil {
		return err
	}
	if n > 0 {
		return errStatus(http.StatusConflict, "setup has already been completed")
	}
	id, err := s.createUser(r, c, true)
	if err != nil {
		return err
	}
	if err := s.startSession(w, r, id); err != nil {
		return err
	}
	return ok(w, map[string]any{"ok": true})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) error {
	var c credentials
	if err := readJSON(r, &c); err != nil {
		return err
	}
	// Throttle guessing: 10 attempts, then one more every 30 seconds, per IP.
	if !s.limits.login.take(clientIP(r, s.cfg.TrustProxy), 1, 10, 1.0/30) {
		w.Header().Set("Retry-After", "30")
		return errStatus(http.StatusTooManyRequests, "too many sign-in attempts; wait a minute and try again")
	}
	var id int64
	var hash string
	err := s.db.R.QueryRowContext(r.Context(),
		`SELECT id, password_hash FROM users WHERE email = ?`, strings.TrimSpace(c.Email),
	).Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		// Burn comparable time so response timing doesn't reveal which emails exist.
		bcrypt.CompareHashAndPassword(dummyHash, []byte(c.Password))
		return errStatus(http.StatusUnauthorized, "incorrect email or password")
	}
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(c.Password)) != nil {
		return errStatus(http.StatusUnauthorized, "incorrect email or password")
	}
	if err := s.startSession(w, r, id); err != nil {
		return err
	}
	return ok(w, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.db.W.ExecContext(r.Context(), `DELETE FROM sessions WHERE token_hash = ?`, hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	return ok(w, map[string]any{"ok": true})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request, u *user) error {
	var body struct {
		Current string `json:"currentPassword"`
		New     string `json:"newPassword"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	var hash string
	if err := s.db.R.QueryRowContext(r.Context(), `SELECT password_hash FROM users WHERE id = ?`, u.ID).Scan(&hash); err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Current)) != nil {
		return badRequest("current password is incorrect")
	}
	c := credentials{Email: u.Email, Name: u.Name, Password: body.New}
	if err := c.validate(false); err != nil {
		return err
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(body.New), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.W.ExecContext(r.Context(), `UPDATE users SET password_hash = ? WHERE id = ?`, string(newHash), u.ID)
	if err != nil {
		return err
	}
	// Sign out every other session.
	current, _ := r.Cookie(sessionCookie)
	_, err = s.db.W.ExecContext(r.Context(),
		`DELETE FROM sessions WHERE user_id = ? AND token_hash != ?`, u.ID, hashToken(current.Value))
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"ok": true})
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request, u *user) error {
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT `+userColumns+` FROM users u ORDER BY u.created_at`)
	if err != nil {
		return err
	}
	defer rows.Close()
	users := []*user{}
	for rows.Next() {
		x, err := scanUser(rows)
		if err != nil {
			return err
		}
		x.Prefs = nil
		users = append(users, x)
	}
	return ok(w, users)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request, u *user) error {
	if !u.IsAdmin {
		return errForbidden
	}
	var body struct {
		credentials
		IsAdmin bool `json:"isAdmin"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if err := body.validate(true); err != nil {
		return err
	}
	id, err := s.createUser(r, body.credentials, body.IsAdmin)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"id": id})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request, u *user) error {
	if !u.IsAdmin {
		return errForbidden
	}
	id, err := pathInt(r, "uid")
	if err != nil {
		return err
	}
	if id == u.ID {
		return badRequest("you cannot delete your own account")
	}
	if _, err := s.db.W.ExecContext(r.Context(), `DELETE FROM users WHERE id = ?`, id); err != nil {
		return err
	}
	s.audit(r, 0, u, "user.delete", strconv.FormatInt(id, 10))
	return ok(w, map[string]any{"ok": true})
}

var handlePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// uniqueHandle derives an @handle from an email address, adding a number if taken.
func (s *Server) uniqueHandle(r *http.Request, email string) (string, error) {
	local, _, _ := strings.Cut(strings.ToLower(email), "@")
	base := strings.Map(func(c rune) rune {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' {
			return c
		}
		return -1
	}, local)
	if base == "" {
		base = "user"
	}
	if len(base) > 24 {
		base = base[:24]
	}
	for i := 0; i < 1000; i++ {
		h := base
		if i > 0 {
			h += strconv.Itoa(i + 1)
		}
		var n int
		if err := s.db.R.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users WHERE handle = ?`, h).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			return h, nil
		}
	}
	return base + randomHex(3), nil
}

var avatarPattern = regexp.MustCompile(`^data:image/(png|jpeg|webp);base64,[A-Za-z0-9+/]+=*$`)

func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request, u *user) error {
	var body struct {
		Name   *string          `json:"name"`
		Handle *string          `json:"handle"`
		Title  *string          `json:"title"`
		Avatar *string          `json:"avatar"`
		Prefs  *json.RawMessage `json:"prefs"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if body.Name != nil {
		u.Name = strings.TrimSpace(*body.Name)
		if u.Name == "" || len(u.Name) > 80 {
			return badRequest("name must be 1-80 characters")
		}
	}
	if body.Handle != nil {
		h := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(*body.Handle), "@"))
		if !handlePattern.MatchString(h) {
			return badRequest("handles are 1-32 characters: lowercase letters, numbers, - and _")
		}
		var n int
		if err := s.db.R.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users WHERE handle = ? AND id != ?`, h, u.ID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return errStatus(http.StatusConflict, "@%s is taken", h)
		}
		u.Handle = h
	}
	if body.Title != nil {
		u.Title = truncate(strings.TrimSpace(*body.Title), 80)
	}
	if body.Avatar != nil {
		a := *body.Avatar
		if a != "" && (len(a) > 200_000 || !avatarPattern.MatchString(a)) {
			return badRequest("avatar must be a PNG, JPEG or WebP image under 150 KB")
		}
		u.Avatar = a
	}
	if body.Prefs != nil {
		raw := strings.TrimSpace(string(*body.Prefs))
		if len(raw) > 4096 || !strings.HasPrefix(raw, "{") || !json.Valid([]byte(raw)) {
			return badRequest("prefs must be a JSON object under 4 KB")
		}
		u.Prefs = json.RawMessage(raw)
	}
	_, err := s.db.W.ExecContext(r.Context(), `UPDATE users SET name = ?, handle = ?, title = ?, avatar = ?, prefs = ? WHERE id = ?`,
		u.Name, u.Handle, u.Title, u.Avatar, string(u.Prefs), u.ID)
	if err != nil {
		return err
	}
	return ok(w, u)
}

func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request, _ *user) error {
	id, err := pathInt(r, "uid")
	if err != nil {
		return err
	}
	u, err := scanUser(s.db.R.QueryRowContext(r.Context(), `SELECT `+userColumns+` FROM users u WHERE u.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	u.Prefs = nil
	type issueRef struct {
		ID        int64  `json:"id"`
		ProjectID int64  `json:"projectId"`
		Project   string `json:"project"`
		Title     string `json:"title"`
		Status    string `json:"status"`
		LastSeen  int64  `json:"lastSeen"`
	}
	assigned := []issueRef{}
	rows, err := s.db.R.QueryContext(r.Context(), `
		SELECT i.id, i.project_id, p.name, i.title, i.status, i.last_seen FROM issues i JOIN projects p ON p.id = i.project_id
		WHERE i.assignee_id = ? AND i.status = 'unresolved' ORDER BY i.last_seen DESC LIMIT 50`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var x issueRef
		if err := rows.Scan(&x.ID, &x.ProjectID, &x.Project, &x.Title, &x.Status, &x.LastSeen); err != nil {
			rows.Close()
			return err
		}
		assigned = append(assigned, x)
	}
	rows.Close()
	type activity struct {
		Kind      string `json:"kind"`
		Body      string `json:"body"`
		CreatedAt int64  `json:"createdAt"`
		IssueID   int64  `json:"issueId"`
		ProjectID int64  `json:"projectId"`
		Title     string `json:"title"`
	}
	recent := []activity{}
	rows, err = s.db.R.QueryContext(r.Context(), `
		SELECT a.kind, a.body, a.created_at, i.id, i.project_id, i.title FROM issue_activity a JOIN issues i ON i.id = a.issue_id
		WHERE a.user_id = ? ORDER BY a.created_at DESC, a.id DESC LIMIT 30`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var x activity
		if err := rows.Scan(&x.Kind, &x.Body, &x.CreatedAt, &x.IssueID, &x.ProjectID, &x.Title); err != nil {
			return err
		}
		x.Body = truncate(x.Body, 280)
		recent = append(recent, x)
	}
	return ok(w, map[string]any{"user": u, "assigned": assigned, "activity": recent})
}

func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request, u *user) error {
	rows, err := s.db.R.QueryContext(r.Context(), `
		SELECT n.id, n.kind, n.body, n.created_at, n.read_at, COALESCE(n.project_id, 0), COALESCE(n.issue_id, 0),
			COALESCE(i.title, ''), COALESCE(a.name, ''), COALESCE(a.avatar, ''), COALESCE(a.handle, '')
		FROM notifications n
		LEFT JOIN issues i ON i.id = n.issue_id
		LEFT JOIN users a ON a.id = n.actor_id
		WHERE n.user_id = ? ORDER BY n.created_at DESC, n.id DESC LIMIT 50`, u.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type note struct {
		ID          int64  `json:"id"`
		Kind        string `json:"kind"`
		Body        string `json:"body"`
		CreatedAt   int64  `json:"createdAt"`
		ReadAt      *int64 `json:"readAt"`
		ProjectID   int64  `json:"projectId"`
		IssueID     int64  `json:"issueId"`
		IssueTitle  string `json:"issueTitle"`
		ActorName   string `json:"actorName"`
		ActorAvatar string `json:"actorAvatar"`
		ActorHandle string `json:"actorHandle"`
	}
	out := []note{}
	unread := 0
	for rows.Next() {
		var x note
		if err := rows.Scan(&x.ID, &x.Kind, &x.Body, &x.CreatedAt, &x.ReadAt, &x.ProjectID, &x.IssueID, &x.IssueTitle, &x.ActorName, &x.ActorAvatar, &x.ActorHandle); err != nil {
			return err
		}
		if x.ReadAt == nil {
			unread++
		}
		out = append(out, x)
	}
	return ok(w, map[string]any{"notifications": out, "unread": unread})
}

func (s *Server) handleReadNotifications(w http.ResponseWriter, r *http.Request, u *user) error {
	var body struct {
		IDs []int64 `json:"ids"` // empty = all
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	now := nowMs()
	if len(body.IDs) == 0 {
		_, err := s.db.W.ExecContext(r.Context(), `UPDATE notifications SET read_at = ? WHERE user_id = ? AND read_at IS NULL`, now, u.ID)
		if err != nil {
			return err
		}
	}
	for _, id := range body.IDs {
		if _, err := s.db.W.ExecContext(r.Context(), `UPDATE notifications SET read_at = ? WHERE id = ? AND user_id = ? AND read_at IS NULL`, now, id, u.ID); err != nil {
			return err
		}
	}
	return ok(w, map[string]any{"ok": true})
}
