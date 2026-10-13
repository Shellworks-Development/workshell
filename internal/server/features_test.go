package server

import (
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
	if code := e.ingestWith(key, env, sign(p.SigningSecret, 0)); code != http.StatusAccepted {
		t.Fatalf("valid signature rejected: %d", code)
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
	if len(issues.Issues) > 5 {
		t.Fatalf("client items beyond the per-player budget should be dropped; got %d issues", len(issues.Issues))
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
