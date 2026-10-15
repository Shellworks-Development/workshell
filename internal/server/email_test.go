package server

import (
	"net/http"
	"net/http/cookiejar"
	"testing"
)

func TestChangeEmail(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	jar, _ := cookiejar.New(nil)
	other := &http.Client{Jar: jar}
	original := e.client
	e.client = other
	if code := e.do("POST", "/api/auth/login", map[string]string{"email": "admin@example.com", "password": "hunter2hunter2"}, nil); code != 200 {
		t.Fatalf("login: %d", code)
	}
	e.client = original
	for _, email := range []string{"invalid", "Name <new@example.com>"} {
		if code := e.do("POST", "/api/auth/email", map[string]string{"email": email, "currentPassword": "hunter2hunter2"}, nil); code != 400 {
			t.Fatalf("invalid email: %d", code)
		}
	}
	if code := e.do("POST", "/api/auth/email", map[string]string{"email": "new@example.com", "currentPassword": "wrong"}, nil); code != 400 {
		t.Fatalf("bad password: %d", code)
	}
	var updated user
	if code := e.do("POST", "/api/auth/email", map[string]string{"email": " new@example.com ", "currentPassword": "hunter2hunter2"}, &updated); code != 200 || updated.Email != "new@example.com" {
		t.Fatalf("change: %d %+v", code, updated)
	}
	if code := e.do("GET", "/api/projects", nil, nil); code != 200 {
		t.Fatalf("current session revoked: %d", code)
	}
	e.client = other
	if code := e.do("GET", "/api/projects", nil, nil); code != 401 {
		t.Fatalf("other session retained: %d", code)
	}
	if code := e.do("POST", "/api/auth/login", map[string]string{"email": "admin@example.com", "password": "hunter2hunter2"}, nil); code != 401 {
		t.Fatalf("old email login: %d", code)
	}
	if code := e.do("POST", "/api/auth/login", map[string]string{"email": "new@example.com", "password": "hunter2hunter2"}, nil); code != 200 {
		t.Fatalf("new email login: %d", code)
	}
}

func TestChangeEmailConflictAndSSO(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	if code := e.do("POST", "/api/users", map[string]string{"email": "taken@example.com", "name": "Taken", "password": "hunter2hunter2"}, nil); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	if code := e.do("POST", "/api/auth/email", map[string]string{"email": "TAKEN@example.com", "currentPassword": "hunter2hunter2"}, nil); code != 409 {
		t.Fatalf("conflict: %d", code)
	}
	var state struct {
		User         user `json:"user"`
		EmailManaged bool `json:"emailManaged"`
	}
	e.do("GET", "/api/auth/state", nil, &state)
	if _, err := e.s.db.W.Exec(`INSERT INTO user_identities (provider,subject,user_id,email,created_at,last_used) VALUES ('oidc:test','subject',?,'admin@example.com',0,0)`, state.User.ID); err != nil {
		t.Fatal(err)
	}
	e.do("GET", "/api/auth/state", nil, &state)
	if !state.EmailManaged {
		t.Fatal("SSO email should be marked managed")
	}
	if code := e.do("POST", "/api/auth/email", map[string]string{"email": "new@example.com", "currentPassword": "hunter2hunter2"}, nil); code != 403 {
		t.Fatalf("SSO email change: %d", code)
	}
	if _, err := e.s.db.W.Exec(`DELETE FROM user_identities`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.db.W.Exec(`UPDATE users SET password_hash = '' WHERE id = ?`, state.User.ID); err != nil {
		t.Fatal(err)
	}
	if code := e.do("POST", "/api/auth/email", map[string]string{"email": "new@example.com", "currentPassword": ""}, nil); code != 403 {
		t.Fatalf("passwordless email change: %d", code)
	}
}
