package server

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/workshell/workshell/internal/secure"
)

func TestSignedIngest(t *testing.T) {
	e := newTestEnv(t)
	pid, key := e.setup()
	base := "/api/projects/" + itoa(pid)
	var p project
	e.do("POST", base+"/signing-secret", map[string]any{}, &p)
	if p.SigningSecret == "" {
		t.Fatal("no signing secret")
	}
	e.do("PATCH", base, map[string]any{"requireSignature": true}, nil)
	e.s.invalidateKeyCache()

	env := map[string]any{"server": map[string]any{"jobId": "j"}, "items": []any{map[string]any{"type": "log", "message": "hi"}}}
	if code := e.ingestWith(key, env, nil); code != http.StatusUnauthorized {
		t.Fatalf("unsigned request should be rejected, got %d", code)
	}
	sign := func(secret string, skew time.Duration) func([]byte) map[string]string {
		return func(body []byte) map[string]string {
			ts := strconv.FormatInt(time.Now().Add(skew).Unix(), 10)
			return map[string]string{
				"X-Workshell-Timestamp": ts,
				"X-Workshell-Signature": "v1=" + secure.HMACHex([]byte(secret), append([]byte(ts+"."), body...)),
			}
		}
	}
	if code := e.ingestWith(key, env, sign("wrong", 0)); code != http.StatusUnauthorized {
		t.Fatalf("bad signature should be rejected, got %d", code)
	}
	if code := e.ingestWith(key, env, sign(p.SigningSecret, -10*time.Minute)); code != http.StatusUnauthorized {
		t.Fatalf("stale timestamp should be rejected, got %d", code)
	}
	valid := sign(p.SigningSecret, 0)
	body, _ := json.Marshal(env)
	headers := valid(body)
	fixed := func([]byte) map[string]string { return headers }
	if code := e.ingestWith(key, env, fixed); code != http.StatusAccepted {
		t.Fatalf("valid signature rejected: %d", code)
	}
	// The same signed request again inside the window is a replay.
	if code := e.ingestWith(key, env, fixed); code != http.StatusConflict {
		t.Fatalf("replayed request should be rejected, got %d", code)
	}
}

// "Require signed requests" with no secret must fail closed, not accept any
// signature header (it used to skip verification entirely).
func TestRequireSignatureWithoutSecretFailsClosed(t *testing.T) {
	e := newTestEnv(t)
	pid, key := e.setup()
	e.do("PATCH", "/api/projects/"+itoa(pid), map[string]any{"requireSignature": true}, nil)
	// The API refuses this combination; set it directly to test the defense in depth.
	e.s.db.W.Exec(`UPDATE projects SET signing_secret = '', require_signature = 1 WHERE id = ?`, pid)
	e.s.invalidateKeyCache()
	env := map[string]any{"server": map[string]any{"jobId": "j"}, "items": []any{map[string]any{"type": "log", "message": "hi"}}}
	forged := func([]byte) map[string]string {
		return map[string]string{"X-Workshell-Timestamp": strconv.FormatInt(time.Now().Unix(), 10), "X-Workshell-Signature": "v1=deadbeef"}
	}
	if code := e.ingestWith(key, env, forged); code != http.StatusUnauthorized {
		t.Fatalf("forged signature accepted with no secret configured: %d", code)
	}
}

// A batch delivered twice (written, then redelivered before it was
// acknowledged) is stored once.
func TestRedeliveredBatchIsIngestedOnce(t *testing.T) {
	e := newTestEnv(t)
	pid, _ := e.setup()
	body := []byte(`{"server":{"jobId":"j"},"items":[{"type":"log","message":"exactly once"}]}`)
	job := make([]byte, 16+len(body))
	binary.BigEndian.PutUint64(job, uint64(pid))
	binary.BigEndian.PutUint64(job[8:], uint64(time.Now().UnixMilli()))
	copy(job[16:], body)
	for i := 0; i < 2; i++ {
		if errs := e.s.processJobs(context.Background(), [][]byte{job}); errs[0] != nil {
			t.Fatalf("delivery %d failed: %v", i+1, errs[0])
		}
	}
	var n int
	e.s.db.R.QueryRow(`SELECT COUNT(*) FROM logs WHERE project_id = ? AND message = 'exactly once'`, pid).Scan(&n)
	if n != 1 {
		t.Fatalf("expected the log once, got %d", n)
	}
}

func TestHashedUserIDs(t *testing.T) {
	e := newTestEnv(t)
	pid, key := e.setup()
	base := "/api/projects/" + itoa(pid)
	e.do("PATCH", base, map[string]any{"hashUserIds": true}, nil)
	e.s.invalidateKeyCache()
	e.ingest(key, map[string]any{"server": map[string]any{"jobId": "j"}, "items": []any{
		map[string]any{"type": "session_start", "userId": 12345, "sessionId": "s1"},
	}})
	var raw struct{ Player struct{ UserID int64 } }
	if code := e.do("GET", base+"/players/12345", nil, &raw); code != http.StatusNotFound {
		t.Fatalf("raw id should not be stored, got %d", code)
	}
	if code := e.do("GET", base+"/players/12345?raw=1", nil, &raw); code != 200 || raw.Player.UserID == 12345 || raw.Player.UserID <= 0 {
		t.Fatalf("lookup by Roblox id should find the pseudonymous player: %d %+v", code, raw)
	}
}

func TestClientFloodIsLimitedAndFlagged(t *testing.T) {
	e := newTestEnv(t)
	pid, key := e.setup()
	base := "/api/projects/" + itoa(pid)
	e.do("PATCH", base, map[string]any{"clientRate": 5}, nil)
	e.s.invalidateKeyCache()
	items := []any{}
	for i := 0; i < 50; i++ {
		items = append(items, map[string]any{"type": "error", "side": "client", "userId": 9, "message": "spam " + strconv.Itoa(i)})
	}
	e.ingest(key, map[string]any{"server": map[string]any{"jobId": "j"}, "items": items})
	var issues struct{ Issues []issueRow }
	e.do("GET", base+"/issues?limit=100", nil, &issues)
	if len(issues.Issues) != 0 {
		t.Fatalf("a flooding player's issues shouldn't stay in the unresolved list; got %d", len(issues.Issues))
	}
	e.do("GET", base+"/issues?status=ignored&limit=100", nil, &issues)
	if len(issues.Issues) == 0 || len(issues.Issues) > 5 {
		t.Fatalf("expected the few issues that got through to be auto-ignored; got %d", len(issues.Issues))
	}
	var activity []activityRow
	e.do("GET", base+"/issues/"+itoa(issues.Issues[0].ID)+"/activity", nil, &activity)
	if activity[len(activity)-1].Kind != "quarantine" {
		t.Fatalf("expected a quarantine note, got %+v", activity)
	}

	// A legitimate client error from someone else is unaffected.
	e.ingest(key, map[string]any{"server": map[string]any{"jobId": "j"}, "items": []any{
		map[string]any{"type": "error", "side": "client", "userId": 10, "message": "real client bug"},
	}})
	e.do("GET", base+"/issues", nil, &issues)
	if len(issues.Issues) != 1 || issues.Issues[0].Title != "real client bug" {
		t.Fatalf("legitimate client issue should stay open: %+v", issues.Issues)
	}
	// And the flagged player's later client errors start out ignored.
	e.ingest(key, map[string]any{"server": map[string]any{"jobId": "j"}, "items": []any{
		map[string]any{"type": "error", "side": "client", "userId": 9, "message": "totally real error, trust me", "ts": time.Now().Add(2 * time.Minute).UnixMilli()},
	}})
	e.do("GET", base+"/issues", nil, &issues)
	if len(issues.Issues) != 1 {
		t.Fatalf("flagged player's new client issue should start ignored: %+v", issues.Issues)
	}
	var sec struct {
		Recent []detectionRow
	}
	e.do("GET", base+"/security", nil, &sec)
	if len(sec.Recent) != 1 || sec.Recent[0].Kind != "client_flood" || sec.Recent[0].Source != "workshell" {
		t.Fatalf("expected one automatic client_flood detection, got %+v", sec.Recent)
	}
}

func TestCommentsMentionsAndNotifications(t *testing.T) {
	e := newTestEnv(t)
	pid, key := e.setup()
	base := "/api/projects/" + itoa(pid)
	var created struct{ ID int64 }
	e.do("POST", "/api/users", map[string]any{"name": "Bob", "email": "bob@example.com", "password": "hunter2hunter2"}, &created)
	e.ingest(key, map[string]any{"server": map[string]any{"jobId": "j"}, "items": []any{
		map[string]any{"type": "error", "message": "boom"},
	}})
	var issues struct{ Issues []issueRow }
	e.do("GET", base+"/issues", nil, &issues)
	iid := itoa(issues.Issues[0].ID)

	if code := e.do("POST", base+"/issues/"+iid+"/comments", map[string]any{"body": "@bob can you take a look? <script>alert(1)</script>"}, nil); code != 200 {
		t.Fatalf("comment: %d", code)
	}
	e.do("PATCH", base+"/issues/"+iid, map[string]any{"assigneeId": created.ID, "status": "resolved"}, nil)

	var activity []activityRow
	e.do("GET", base+"/issues/"+iid+"/activity", nil, &activity)
	kinds := []string{}
	for _, a := range activity {
		kinds = append(kinds, a.Kind)
	}
	if len(kinds) != 4 || kinds[0] != "first_seen" || kinds[1] != "comment" {
		t.Fatalf("unexpected activity: %v", kinds)
	}

	// Log in as Bob and check his inbox.
	e.do("POST", "/api/auth/logout", map[string]any{}, nil)
	e.do("POST", "/api/auth/login", map[string]any{"email": "bob@example.com", "password": "hunter2hunter2"}, nil)
	var inbox struct {
		Unread        int
		Notifications []struct{ Kind string }
	}
	e.do("GET", "/api/notifications", nil, &inbox)
	if inbox.Unread != 2 || inbox.Notifications[0].Kind != "assigned" || inbox.Notifications[1].Kind != "mention" {
		t.Fatalf("unexpected inbox: %+v", inbox)
	}
}

func TestReleaseSourcemapResolvesFrames(t *testing.T) {
	e := newTestEnv(t)
	pid, key := e.setup()
	base := "/api/projects/" + itoa(pid)
	e.do("PATCH", base, map[string]any{"githubRepo": "acme/game"}, nil)
	smap := map[string]any{"name": "Game", "className": "DataModel", "children": []any{
		map[string]any{"name": "ServerScriptService", "className": "ServerScriptService", "children": []any{
			map[string]any{"name": "Combat", "className": "Script", "filePaths": []string{"src/server/Combat.server.luau"}},
		}},
		map[string]any{"name": "StarterGui", "className": "StarterGui", "children": []any{
			map[string]any{"name": "Shop", "className": "LocalScript", "filePaths": []string{"src/client/Shop.client.luau"}},
		}},
	}}
	body, _ := json.Marshal(map[string]any{"version": "abc123", "commit": "abc123def", "sourcemap": smap})
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/releases", bytesReader(body))
	req.Header.Set("X-Workshell-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("create release: %v %v", err, resp.Status)
	}
	e.ingest(key, map[string]any{"server": map[string]any{"jobId": "j", "release": "abc123"}, "items": []any{
		map[string]any{"type": "error", "message": "ServerScriptService.Combat:10: x", "stack": "ServerScriptService.Combat, line 10 - function f\n"},
		map[string]any{"type": "error", "side": "client", "message": "Players.bob.PlayerGui.Shop:3: y", "stack": "Players.bob.PlayerGui.Shop, line 3\n"},
	}})
	var issues struct{ Issues []issueRow }
	e.do("GET", base+"/issues", nil, &issues)
	for _, iss := range issues.Issues {
		var events []errorEvent
		e.do("GET", base+"/issues/"+itoa(iss.ID)+"/events", nil, &events)
		f := events[0].Frames[0]
		if f.File == "" || f.URL == "" {
			t.Fatalf("frame not resolved: %+v", f)
		}
		if iss.Side == "client" && f.File != "src/client/Shop.client.luau" {
			t.Fatalf("client frame resolved to %q", f.File)
		}
	}
	var rel struct {
		Release struct {
			CommitURL string
			NewIssues int
		}
	}
	e.do("GET", base+"/releases/abc123", nil, &rel)
	if rel.Release.NewIssues != 2 || rel.Release.CommitURL != "https://github.com/acme/game/commit/abc123def" {
		t.Fatalf("unexpected release: %+v", rel)
	}
}

func TestSessionTimeline(t *testing.T) {
	e := newTestEnv(t)
	pid, key := e.setup()
	base := "/api/projects/" + itoa(pid)
	e.ingest(key, map[string]any{"server": map[string]any{"jobId": "j"}, "items": []any{
		map[string]any{"type": "session_start", "userId": 5, "sessionId": "sess-1"},
		map[string]any{"type": "event", "userId": 5, "sessionId": "sess-1", "name": "level_up"},
		map[string]any{"type": "log", "userId": 5, "message": "untagged but same player"},
		map[string]any{"type": "span", "userId": 5, "sessionId": "sess-1", "name": "datastore.load", "durationMs": 120},
		map[string]any{"type": "detection", "userId": 5, "sessionId": "sess-1", "kind": "speed", "severity": "high", "action": "kicked"},
		map[string]any{"type": "error", "userId": 5, "sessionId": "sess-1", "message": "oops"},
		map[string]any{"type": "client_perf", "sessionId": "sess-1", "fps": 58, "pingMs": 90, "memoryMb": 1200, "platform": "Phone"},
		map[string]any{"type": "session_end", "sessionId": "sess-1"},
	}})
	var res struct {
		Session  sessionRow
		Timeline []timelineItem
		Perf     []map[string]any
	}
	if code := e.do("GET", base+"/sessions/sess-1", nil, &res); code != 200 {
		t.Fatalf("session: %d", code)
	}
	got := map[string]bool{}
	for _, it := range res.Timeline {
		got[it.Kind] = true
	}
	for _, k := range []string{"join", "event", "log", "span", "detection", "error", "leave"} {
		if !got[k] {
			t.Errorf("timeline missing %s: %+v", k, res.Timeline)
		}
	}
	if res.Session.ErrorCount != 1 || len(res.Perf) != 1 {
		t.Fatalf("errorCount=%d perf=%d", res.Session.ErrorCount, len(res.Perf))
	}
}

func TestOneCharacterHandle(t *testing.T) {
	e := newTestEnv(t)
	pid, key := e.setup()
	base := "/api/projects/" + itoa(pid)
	if code := e.do("PATCH", "/api/me", map[string]any{"handle": "a"}, nil); code != 200 {
		t.Fatalf("1-character handle rejected: %d", code)
	}
	e.do("POST", "/api/users", map[string]any{"name": "Bob", "email": "bob@example.com", "password": "hunter2hunter2"}, nil)
	e.ingest(key, map[string]any{"server": map[string]any{"jobId": "j"}, "items": []any{map[string]any{"type": "error", "message": "boom"}}})
	var issues struct{ Issues []issueRow }
	e.do("GET", base+"/issues", nil, &issues)

	e.do("POST", "/api/auth/logout", map[string]any{}, nil)
	e.do("POST", "/api/auth/login", map[string]any{"email": "bob@example.com", "password": "hunter2hunter2"}, nil)
	e.do("POST", base+"/issues/"+itoa(issues.Issues[0].ID)+"/comments", map[string]any{"body": "@a this one's yours (mail me at bob@a too)"}, nil)

	e.do("POST", "/api/auth/logout", map[string]any{}, nil)
	e.do("POST", "/api/auth/login", map[string]any{"email": "admin@example.com", "password": "hunter2hunter2"}, nil)
	var inbox struct {
		Unread        int
		Notifications []struct{ Kind string }
	}
	e.do("GET", "/api/notifications", nil, &inbox)
	if inbox.Unread != 1 || inbox.Notifications[0].Kind != "mention" {
		t.Fatalf("expected one mention for @a, got %+v", inbox)
	}
}

// A crash loop across a fleet keeps issueSamples full events and counts the
// rest, so totals, charts and breakdowns still add up.
func TestErrorFloodKeepsSamplesAndCounts(t *testing.T) {
	e := newTestEnv(t)
	pid, key := e.setup()
	base := "/api/projects/" + itoa(pid)
	now := time.Now().UnixMilli()
	for b := 0; b < 3; b++ {
		items := []any{}
		for i := 0; i < 20; i++ {
			items = append(items, map[string]any{"type": "error", "message": "attempt to index nil with 'Price'",
				"stack": "ReplicatedStorage.Shop:210", "ts": now + int64(b*20+i)})
		}
		env := map[string]any{"server": map[string]any{"jobId": "job-" + itoa(int64(b)), "placeVersion": 42}, "items": items}
		if code := e.ingest(key, env); code != http.StatusAccepted {
			t.Fatalf("ingest: %d", code)
		}
	}
	e.settle()

	var list struct {
		Issues []struct {
			ID         int64 `json:"id"`
			EventCount int64 `json:"eventCount"`
		} `json:"issues"`
	}
	e.do("GET", base+"/issues", nil, &list)
	issues := list.Issues
	if len(issues) != 1 || issues[0].EventCount != 60 {
		t.Fatalf("expected one issue with 60 events, got %+v", issues)
	}
	var stored, tallied int64
	e.s.db.R.QueryRow(`SELECT COUNT(*) FROM error_events WHERE issue_id = ? AND sampled = 1`, issues[0].ID).Scan(&stored)
	e.s.db.R.QueryRow(`SELECT COALESCE(SUM(count), 0) FROM error_events WHERE issue_id = ? AND sampled = 0`, issues[0].ID).Scan(&tallied)
	if stored != issueSamples || tallied != 60-issueSamples {
		t.Fatalf("expected %d samples and %d tallied, got %d and %d", issueSamples, 60-issueSamples, stored, tallied)
	}
	var events []errorEvent
	e.do("GET", base+"/issues/"+itoa(issues[0].ID)+"/events?limit=100", nil, &events)
	if len(events) != issueSamples || events[0].Stack == "" {
		t.Fatalf("events browser should list only the %d samples, got %d", issueSamples, len(events))
	}
	var detail struct {
		Breakdown map[string][]tagCount `json:"breakdown"`
	}
	e.do("GET", base+"/issues/"+itoa(issues[0].ID), nil, &detail)
	var servers int64
	for _, t := range detail.Breakdown["server"] {
		servers += t.Count
	}
	if servers != 60 {
		t.Fatalf("server breakdown should cover all 60 events, got %d", servers)
	}
}
