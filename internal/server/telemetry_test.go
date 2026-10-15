package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/workshell/workshell/internal/db"
	"github.com/workshell/workshell/internal/telemetry"
)

func TestClickHouseOutboxRetries(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	var delivered atomic.Int64
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Query().Get("query"), "INSERT") {
			if fail.Load() {
				http.Error(w, "unavailable", 503)
				return
			}
			dec := json.NewDecoder(r.Body)
			for {
				var row telemetry.Row
				err := dec.Decode(&row)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Error(err)
					break
				}
				if row["project_id"] == nil || row["id"] == nil {
					t.Error("missing routing/id")
				}
				delivered.Add(1)
			}
		}
	}))
	defer remote.Close()
	warehouse, err := telemetry.Open(context.Background(), remote.URL, "default", "default", "")
	if err != nil {
		t.Fatal(err)
	}
	defer warehouse.Close()
	e := newTestEnvOptions(t, func(o *Options) { o.Telemetry = warehouse })
	_, key := e.setup()
	env := map[string]any{"server": map[string]any{"jobId": "test"}, "items": []any{
		map[string]any{"type": "log", "message": "durable", "userId": 42},
		map[string]any{"type": "metric", "name": "requests", "kind": "counter", "count": 1, "sum": 2, "min": 2, "max": 2, "last": 2},
		map[string]any{"type": "heartbeat", "fps": 60},
		map[string]any{"type": "span", "name": "operation", "durationMs": 12},
		map[string]any{"type": "client_perf", "sessionId": "session", "fps": 60},
	}}
	if code := e.ingest(key, env); code != 202 {
		t.Fatal(code)
	}
	var n int
	if err := e.s.db.R.QueryRow(`SELECT COUNT(*) FROM telemetry_outbox`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("missing durable outbox: %d %v", n, err)
	}
	for _, table := range telemetry.Tables {
		if err := e.s.db.R.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s still written to SQLite: %d %v", table, n, err)
		}
	}
	if err := e.s.flushTelemetry(context.Background()); err == nil {
		t.Fatal("failed insert acknowledged")
	}
	if err := e.s.db.R.QueryRow(`SELECT COUNT(*) FROM telemetry_outbox`).Scan(&n); err != nil || n != 1 {
		t.Fatal("failed outbox removed")
	}
	fail.Store(false)
	if err := e.s.flushTelemetry(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.s.db.R.QueryRow(`SELECT COUNT(*) FROM telemetry_outbox`).Scan(&n); err != nil || n != 0 {
		t.Fatal("outbox not acknowledged")
	}
	if delivered.Load() != 5 {
		t.Fatalf("delivered %d rows", delivered.Load())
	}
	e.ingest(key, env)
	if err := e.s.flushTelemetry(context.Background()); err != nil {
		t.Fatal(err)
	}
	if delivered.Load() != 5 {
		t.Fatal("SDK resend duplicated telemetry")
	}
}

// Run against a disposable ClickHouse instance, e.g.:
// WORKSHELL_TEST_CLICKHOUSE_URL=http://localhost:8123 go test ./internal/server -run TestClickHouseIntegration
// The default user must be allowed to create/drop a uniquely named test database.
func TestClickHouseIntegration(t *testing.T) {
	warehouse := testClickHouse(t)
	e := newTestEnvOptions(t, func(o *Options) { o.Telemetry = warehouse })
	if err := e.s.InitTelemetry(context.Background()); err != nil {
		t.Fatal(err)
	}
	pid, key := e.setup()
	ts := nowMs() - 1000
	env := map[string]any{"server": map[string]any{"jobId": "test-job"}, "items": []any{
		map[string]any{"type": "session_start", "userId": 42, "sessionId": "session", "ts": ts},
		map[string]any{"type": "log", "message": "HeLLo 100%_\\world", "userId": 42, "sessionId": "session", "ts": ts},
		map[string]any{"type": "metric", "name": "requests", "kind": "counter", "count": 2, "sum": 8, "min": 3, "max": 5, "last": 5, "ts": ts},
		map[string]any{"type": "heartbeat", "fps": 60, "players": 3, "ts": ts},
		map[string]any{"type": "span", "name": "operation", "durationMs": 12, "traceId": "trace", "spanId": "root", "userId": 42, "sessionId": "session", "ts": ts},
		map[string]any{"type": "client_perf", "sessionId": "session", "fps": 60, "ts": ts},
	}}
	if code := e.ingest(key, env); code != 202 {
		t.Fatal(code)
	}
	if err := e.s.flushTelemetry(context.Background()); err != nil {
		t.Fatal(err)
	}
	prefix := fmt.Sprintf("/api/projects/%d", pid)
	for _, path := range []string{
		"/logs?histogram=1&q=hello", "/logs?q=100%25_", "/metrics", "/metrics/series?name=requests", "/performance", "/performance/spans?name=operation", "/traces/trace", "/servers/stats", "/servers/test-job", "/sessions/session", "/players/42", "/overview",
	} {
		t.Run(path, func(t *testing.T) {
			var body json.RawMessage
			if code := e.do("GET", prefix+path, nil, &body); code != 200 {
				t.Fatalf("%s: %d %s", path, code, body)
			}
			if strings.Contains(path, "/logs?") && !strings.Contains(string(body), "HeLLo") {
				t.Fatalf("case/escape search lost log: %s", body)
			}
		})
	}
	// Erasure must remove both direct user tags and session-only samples.
	if _, err := e.s.erasePlayer(context.Background(), pid, []int64{42}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"logs", "spans", "client_perf"} {
		rows, err := warehouse.Query(context.Background(), `SELECT COUNT(*) FROM `+table+` WHERE project_id = ?`, pid)
		if err != nil {
			t.Fatal(err)
		}
		var n int64
		if !rows.Next() {
			t.Fatal("no count")
		}
		err = rows.Scan(&n)
		rows.Close()
		if err != nil || n != 0 {
			t.Fatalf("erasure left %s: %d %v", table, n, err)
		}
	}
	// Empty FPS aggregates must remain zero rather than NaN.
	rows, err := warehouse.Query(context.Background(), `SELECT COALESCE(AVG(fps), 0), COUNT(DISTINCT job_id) FROM heartbeats WHERE project_id = ?`, pid+1)
	if err != nil {
		t.Fatal(err)
	}
	var avg float64
	var live int
	if !rows.Next() {
		t.Fatal("no empty aggregate")
	}
	err = rows.Scan(&avg, &live)
	rows.Close()
	if err != nil || avg != 0 || live != 0 {
		t.Fatalf("empty aggregate: %v %d %v", avg, live, err)
	}
	// Duplicate external delivery is visible once before background merges.
	oldTS := nowMs() - 100*dayMs
	for _, table := range telemetry.Tables {
		old := []telemetry.Row{{"id": int64(9000), "project_id": pid, "ts": oldTS}}
		if err := warehouse.Insert(context.Background(), table, old); err != nil {
			t.Fatal(err)
		}
		if err := warehouse.Insert(context.Background(), table, old); err != nil {
			t.Fatal(err)
		}
		rows, err := warehouse.Query(context.Background(), `SELECT COUNT(*) FROM `+table+` WHERE project_id = ? AND id = ?`, pid, int64(9000))
		if err != nil {
			t.Fatal(err)
		}
		var n int64
		if !rows.Next() {
			t.Fatal("no duplicate count")
		}
		err = rows.Scan(&n)
		rows.Close()
		if err != nil || n != 1 {
			t.Fatalf("retry duplicated %s: %d %v", table, n, err)
		}
	}
	e.s.runRetention(context.Background())
	for _, table := range telemetry.Tables {
		rows, err := warehouse.Query(context.Background(), `SELECT COUNT(*) FROM `+table+` WHERE project_id = ? AND ts < ?`, pid, oldTS+1)
		if err != nil {
			t.Fatal(err)
		}
		var n int64
		if !rows.Next() {
			t.Fatal("no retention count")
		}
		err = rows.Scan(&n)
		rows.Close()
		if err != nil || n != 0 {
			t.Fatalf("retention left %s: %d %v", table, n, err)
		}
	}
	if code := e.do("DELETE", prefix, nil, nil); code != 200 {
		t.Fatalf("delete project: %d", code)
	}
	for _, table := range telemetry.Tables {
		rows, err := warehouse.Query(context.Background(), `SELECT COUNT(*) FROM `+table+` WHERE project_id = ?`, pid)
		if err != nil {
			t.Fatal(err)
		}
		var n int64
		if !rows.Next() {
			t.Fatal("no deletion count")
		}
		err = rows.Scan(&n)
		rows.Close()
		if err != nil || n != 0 {
			t.Fatalf("project deletion left %s: %d %v", table, n, err)
		}
	}

}

func testClickHouse(t *testing.T) *telemetry.ClickHouse {
	t.Helper()
	endpoint := os.Getenv("WORKSHELL_TEST_CLICKHOUSE_URL")
	if endpoint == "" {
		t.Skip("set WORKSHELL_TEST_CLICKHOUSE_URL for real ClickHouse integration")
	}
	name := "workshell_test_" + randomHex(6)
	req, err := http.NewRequest("POST", strings.TrimRight(endpoint, "/")+"/?query="+url.QueryEscape("CREATE DATABASE "+name), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-ClickHouse-User", "default")
	req.Header.Set("X-ClickHouse-Key", os.Getenv("WORKSHELL_TEST_CLICKHOUSE_PASSWORD"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create database: %s", b)
	}
	warehouse, err := telemetry.Open(context.Background(), endpoint, name, "default", os.Getenv("WORKSHELL_TEST_CLICKHOUSE_PASSWORD"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { warehouse.Exec(context.Background(), "DROP DATABASE "+name); warehouse.Close() })
	return warehouse
}

func TestClickHouseMigration(t *testing.T) {
	warehouse := testClickHouse(t)
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.W.Exec(`INSERT INTO projects (id,name,ingest_key,created_at) VALUES (1,'legacy','key',0)`); err != nil {
		t.Fatal(err)
	}
	ts := nowMs() - 100000
	for _, q := range []string{
		`INSERT INTO logs (id,project_id,ts,level,message) VALUES (100,1,?,'info','legacy')`,
		`INSERT INTO heartbeats (project_id,job_id,ts,players,fps,heartbeat_ms,memory_mb,ping_ms,instances,send_kbps,recv_kbps) VALUES (1,'job',?,1,60,1,1,1,1,1,1)`,
		`INSERT INTO metrics (project_id,name,ts,kind,count,sum,min,max,last) VALUES (1,'legacy',?,'counter',1,1,1,1,1)`,
		`INSERT INTO spans (project_id,ts,name,duration_ms) VALUES (1,?,'legacy',1)`,
		`INSERT INTO client_perf (project_id,session_key,ts,fps,memory_mb,ping_ms) VALUES (1,'legacy',?,60,1,1)`,
	} {
		if _, err := database.W.Exec(q, ts); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{db: database, telemetry: warehouse, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx := context.Background()
	if err := s.InitTelemetry(ctx); err == nil {
		t.Fatal("migration must be explicit")
	}
	s.cfg.ClickHouseMigrate = true
	if err := s.InitTelemetry(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range telemetry.Tables {
		var n int64
		if err := database.R.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("legacy rows remain: %s %d %v", table, n, err)
		}
		rows, err := warehouse.Query(ctx, `SELECT COUNT(*) FROM `+table+` WHERE project_id = ?`, int64(1))
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal("missing migration count")
		}
		err = rows.Scan(&n)
		rows.Close()
		if err != nil || n != 1 {
			t.Fatalf("migration lost rows: %s %d %v", table, n, err)
		}
	}
	var next int64
	if err := database.R.QueryRow(`SELECT next_id FROM telemetry_state`).Scan(&next); err != nil || next <= 100 {
		t.Fatalf("id reuse: %d %v", next, err)
	}
	if err := s.InitTelemetry(ctx); err != nil {
		t.Fatal("restart failed", err)
	}
	sqliteOnly := &Server{db: database}
	if err := sqliteOnly.InitTelemetry(ctx); err == nil {
		t.Fatal("silently switched back to SQLite")
	}
}
