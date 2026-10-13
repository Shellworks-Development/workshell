package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/workshell/workshell/internal/config"
	"github.com/workshell/workshell/internal/db"
	"github.com/workshell/workshell/internal/pipeline"
	"github.com/workshell/workshell/internal/secure"
)

type testEnv struct {
	t      *testing.T
	srv    *httptest.Server
	client *http.Client
	s      *Server
}

// settle waits until the ingest queue has been written to the database.
func (e *testEnv) settle() {
	for i := 0; i < 200 && e.s.queue.Pending() > 0; i++ {
		time.Sleep(5 * time.Millisecond)
	}
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	cfg := config.Config{MaxIngestBytes: 1 << 20, SessionTTL: time.Hour}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	box, _ := secure.NewBox(make([]byte, 32))
	queue := pipeline.NewMemQueue(100)
	s := New(Options{Config: cfg, DB: database, Log: log, Web: fstest.MapFS{}, SDK: fstest.MapFS{"init.luau": {Data: []byte("return 1")}},
		Box: box, Queue: queue, Broker: pipeline.NewMemBroker()})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.Run(ctx)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &testEnv{t: t, srv: srv, client: &http.Client{Jar: jar}, s: s}
}

func (e *testEnv) do(method, path string, body any, out any) int {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			e.t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

func (e *testEnv) ingest(key string, env map[string]any) int {
	e.t.Helper()
	defer e.settle()
	return e.ingestWith(key, env, nil)
}

func (e *testEnv) ingestWith(key string, env map[string]any, headers func(body []byte) map[string]string) int {
	e.t.Helper()
	b, _ := json.Marshal(env)
	var extra map[string]string
	if headers != nil {
		extra = headers(b)
	}
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	w.Write(b)
	w.Close()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/api/v1/ingest", &gz)
	req.Header.Set("X-Workshell-Key", key)
	req.Header.Set("Content-Encoding", "gzip")
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// setup creates the admin account and a project, returning its id and ingest key.
func (e *testEnv) setup() (int64, string) {
	e.t.Helper()
	if code := e.do("POST", "/api/auth/setup", map[string]string{"email": "admin@example.com", "name": "Admin", "password": "hunter2hunter2"}, nil); code != 200 {
		e.t.Fatalf("setup: %d", code)
	}
	var created struct{ ID int64 }
	e.do("POST", "/api/projects", map[string]string{"name": "Game"}, &created)
	var p project
	e.do("GET", "/api/projects/"+itoa(created.ID), nil, &p)
	return p.ID, p.IngestKey
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestAuthRequired(t *testing.T) {
	e := newTestEnv(t)
	if code := e.do("GET", "/api/projects", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", code)
	}
	e.setup()
	if code := e.do("POST", "/api/auth/setup", map[string]string{"email": "x@example.com", "name": "X", "password": "hunter2hunter2"}, nil); code != http.StatusConflict {
		t.Fatalf("second setup should be rejected, got %d", code)
	}
	e.do("POST", "/api/auth/logout", map[string]any{}, nil)
	if code := e.do("POST", "/api/auth/login", map[string]string{"email": "admin@example.com", "password": "wrong-password"}, nil); code != http.StatusUnauthorized {
		t.Fatalf("bad password should fail, got %d", code)
	}
	if code := e.do("POST", "/api/auth/login", map[string]string{"email": "ADMIN@example.com", "password": "hunter2hunter2"}, nil); code != 200 {
		t.Fatalf("login failed: %d", code)
	}
}

func TestIngestRejectsBadKey(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	if code := e.ingestWith("wsk_nope", map[string]any{"items": []any{}}, nil); code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", code)
	}
}

func TestIngestEndToEnd(t *testing.T) {
	e := newTestEnv(t)
	pid, key := e.setup()
	now := time.Now().UnixMilli()
	server := map[string]any{"jobId": "job-1", "placeId": 1, "placeVersion": 12, "maxPlayers": 10}
	errItem := func(user int, count int) map[string]any {
		return map[string]any{
			"type": "error", "userId": user, "count": count,
			"message": "ServerScriptService.Combat:42: attempt to index nil with 'Ammo'",
			"stack":   "ServerScriptService.Combat, line 42 - function fire\n",
		}
	}
	code := e.ingest(key, map[string]any{"server": server, "items": []any{
		map[string]any{"type": "session_start", "userId": 1, "sessionId": "a", "ts": 1.7e12 + float64(now-1_700_000_000_000)}, // exponent-form number
		map[string]any{"type": "session_start", "userId": 2, "sessionId": "b"},
		map[string]any{"type": "session_start", "userId": 2, "sessionId": "b"}, // duplicate delivery
		map[string]any{"type": "heartbeat", "players": 2, "fps": 60, "memoryMb": 900},
		errItem(1, 1), errItem(2, 4), errItem(2, 1),
		map[string]any{"type": "log", "level": "warn", "message": "low ammo", "attrs": map[string]any{"n": 1}},
		map[string]any{"type": "event", "name": "$purchase", "userId": 1, "value": 99},
		map[string]any{"type": "metric", "name": "shop.opened", "kind": "counter", "count": 2, "sum": 2, "min": 1, "max": 1, "last": 1},
		map[string]any{"type": "future_type_from_newer_sdk"},
	}})
	if code != http.StatusAccepted {
		t.Fatalf("ingest: %d", code)
	}

	base := "/api/projects/" + itoa(pid)
	var issues struct {
		Issues []issueRow
	}
	e.do("GET", base+"/issues", nil, &issues)
	if len(issues.Issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues.Issues))
	}
	iss := issues.Issues[0]
	if iss.EventCount != 6 || iss.UserCount != 2 || iss.FirstRelease != "12" {
		t.Fatalf("unexpected issue counters: %+v", iss)
	}

	// Resolve, then a new occurrence should regress it.
	e.do("PATCH", base+"/issues/"+itoa(iss.ID), map[string]string{"status": "resolved"}, nil)
	e.ingest(key, map[string]any{"server": server, "items": []any{errItem(3, 1)}})
	var detail struct{ Issue issueRow }
	e.do("GET", base+"/issues/"+itoa(iss.ID), nil, &detail)
	if detail.Issue.Status != "unresolved" || detail.Issue.RegressedAt == nil {
		t.Fatalf("expected regression, got %+v", detail.Issue)
	}

	var overview struct {
		LivePlayers, LiveServers, UniquePlayers, NewPlayers, Sessions int
		Revenue                                                       float64
	}
	e.do("GET", base+"/overview", nil, &overview)
	if overview.LivePlayers != 2 || overview.LiveServers != 1 || overview.UniquePlayers != 2 || overview.Sessions != 2 || overview.Revenue != 99 {
		t.Fatalf("unexpected overview: %+v", overview)
	}

	// Closing the server ends open sessions.
	e.ingest(key, map[string]any{"server": server, "items": []any{map[string]any{"type": "server_close"}}})
	var player struct {
		Sessions []struct{ EndedAt *int64 }
	}
	e.do("GET", base+"/players/2", nil, &player)
	if len(player.Sessions) != 1 || player.Sessions[0].EndedAt == nil {
		t.Fatalf("expected one ended session, got %+v", player.Sessions)
	}

	var logs struct{ Logs []logRow }
	e.do("GET", base+"/logs?level=warn", nil, &logs)
	if len(logs.Logs) != 1 || logs.Logs[0].Message != "low ammo" {
		t.Fatalf("unexpected logs: %+v", logs.Logs)
	}

	var funnel struct{ Counts []int }
	e.do("POST", base+"/events/funnel", map[string]any{"steps": []string{"$session", "$purchase"}}, &funnel)
	if len(funnel.Counts) != 2 || funnel.Counts[0] != 2 || funnel.Counts[1] != 1 {
		t.Fatalf("unexpected funnel: %+v", funnel.Counts)
	}
}

func TestSDKModelDownload(t *testing.T) {
	e := newTestEnv(t)
	resp, err := http.Get(e.srv.URL + "/api/sdk/Workshell.rbxmx")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !bytes.Contains(body, []byte(`<string name="Name">Workshell</string>`)) {
		t.Fatalf("bad model: %d %s", resp.StatusCode, body)
	}
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
