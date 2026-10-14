package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func robloxSign(secret string, ts time.Time, body []byte) string {
	t := strconv.FormatInt(ts.Unix(), 10)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(t + "."))
	m.Write(body)
	return "t=" + t + ",v1=" + base64.StdEncoding.EncodeToString(m.Sum(nil))
}

func TestRobloxErasureWebhook(t *testing.T) {
	e := newTestEnv(t)
	pid, key := e.setup()
	base := "/api/projects/" + itoa(pid)
	player := func(uid int, sess string) []any {
		return []any{
			map[string]any{"type": "session_start", "userId": uid, "sessionId": sess},
			map[string]any{"type": "event", "userId": uid, "sessionId": sess, "name": "level_up"},
			map[string]any{"type": "log", "userId": uid, "message": "hello"},
			map[string]any{"type": "span", "sessionId": sess, "name": "datastore.load", "durationMs": 120},
			map[string]any{"type": "detection", "userId": uid, "sessionId": sess, "kind": "speed", "severity": "high"},
			map[string]any{"type": "error", "userId": uid, "sessionId": sess, "message": "shop broke"},
			map[string]any{"type": "client_perf", "sessionId": sess, "fps": 58, "pingMs": 90, "memoryMb": 1200},
			map[string]any{"type": "session_end", "sessionId": sess},
		}
	}
	items := append(player(77, "sess-77"), player(78, "sess-78")...)
	e.ingest(key, map[string]any{"server": map[string]any{"jobId": "j"}, "items": items})

	// A whiteboard snapshot of the player's session goes with them.
	var board struct{ ID int64 }
	e.do("POST", base+"/whiteboards", map[string]any{"title": "Investigation"}, &board)
	now := time.Now().UnixMilli()
	e.s.db.W.Exec(`INSERT INTO whiteboard_items (board_id, id, kind, data, updated_at) VALUES
		(?, 'a', 'embed', '{"type":"session","title":"Player 77 · 3m","link":"/p/1/sessions/sess-77"}', ?),
		(?, 'b', 'embed', '{"type":"session","title":"Player 78 · 3m","link":"/p/1/sessions/sess-78"}', ?),
		(?, 'c', 'sticky', '{"text":"look at this"}', ?)`, board.ID, now, board.ID, now, board.ID, now)

	url := e.srv.URL + "/api/v1/roblox/" + itoa(pid) + "/webhook"
	post := func(body, sig string) int {
		req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(body)))
		if sig != "" {
			req.Header.Set("Roblox-Signature", sig)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	erase := `{"NotificationId":"n-1","EventType":"RightToErasureRequest","EventTime":"2026-10-14T00:00:00Z","EventPayload":{"UserId":77,"GameIds":[1234]}}`

	// No secret configured yet: the endpoint doesn't exist for this project.
	if code := post(erase, robloxSign("x", time.Now(), []byte(erase))); code != http.StatusNotFound {
		t.Fatalf("without a secret: %d", code)
	}
	var sec struct{ Secret string }
	e.do("POST", base+"/roblox-webhook-secret", map[string]any{}, &sec)
	if sec.Secret == "" {
		t.Fatal("no secret")
	}
	for name, sig := range map[string]string{
		"unsigned":  "",
		"wrong key": robloxSign("nope", time.Now(), []byte(erase)),
		"stale":     robloxSign(sec.Secret, time.Now().Add(-time.Hour), []byte(erase)),
	} {
		if code := post(erase, sig); code != http.StatusUnauthorized {
			t.Fatalf("%s request should be refused, got %d", name, code)
		}
	}
	sample := `{"NotificationId":"n-0","EventType":"SampleNotification","EventPayload":{"UserId":1}}`
	if code := post(sample, robloxSign(sec.Secret, time.Now(), []byte(sample))); code != http.StatusOK {
		t.Fatalf("sample notification: %d", code)
	}
	if code := post(erase, robloxSign(sec.Secret, time.Now(), []byte(erase))); code != http.StatusOK {
		t.Fatalf("erasure: %d", code)
	}

	count := func(q string, args ...any) int {
		var n int
		if err := e.s.db.R.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, table := range []string{"logs", "events", "error_events", "detections", "play_sessions", "players"} {
		if n := count(`SELECT COUNT(*) FROM `+table+` WHERE user_id = 77`); n != 0 {
			t.Errorf("%s still has %d rows for the erased player", table, n)
		}
		if n := count(`SELECT COUNT(*) FROM `+table+` WHERE user_id = 78`); n == 0 {
			t.Errorf("%s lost the other player's rows", table)
		}
	}
	for _, table := range []string{"spans", "client_perf"} {
		if n := count(`SELECT COUNT(*) FROM `+table+` WHERE session_key = 'sess-77'`); n != 0 {
			t.Errorf("%s still has the erased session", table)
		}
	}
	if n := count(`SELECT COUNT(*) FROM issue_users WHERE user_id = 77`); n != 0 {
		t.Error("issue link survived")
	}
	if n := count(`SELECT user_count FROM issues`); n != 1 {
		t.Errorf("issue should count one remaining player, got %d", n)
	}
	if n := count(`SELECT COUNT(*) FROM whiteboard_items WHERE board_id = ?`, board.ID); n != 2 {
		t.Errorf("expected the player's snapshot removed and 2 items left, got %d", n)
	}
	if n := count(`SELECT COUNT(*) FROM audit_log WHERE action = 'player.erase' AND detail NOT LIKE '%77%'`); n != 1 {
		t.Error("erasure should be audited without the player id")
	}
}

func TestManualErasureOfHashedPlayer(t *testing.T) {
	e := newTestEnv(t)
	pid, key := e.setup()
	base := "/api/projects/" + itoa(pid)
	e.do("PATCH", base, map[string]any{"hashUserIds": true}, nil)
	e.s.invalidateKeyCache()
	e.ingest(key, map[string]any{"server": map[string]any{"jobId": "j"}, "items": []any{
		map[string]any{"type": "session_start", "userId": 12345, "sessionId": "s"},
		map[string]any{"type": "log", "userId": 12345, "message": "hi"},
	}})
	if code := e.do("DELETE", base+"/players/12345?raw=1", nil, nil); code != http.StatusOK {
		t.Fatalf("erase: %d", code)
	}
	var n int
	e.s.db.R.QueryRow(`SELECT (SELECT COUNT(*) FROM players) + (SELECT COUNT(*) FROM logs) + (SELECT COUNT(*) FROM play_sessions)`).Scan(&n)
	if n != 0 {
		t.Fatalf("hashed player's data survived: %d rows", n)
	}
}
