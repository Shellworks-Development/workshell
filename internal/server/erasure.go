package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/workshell/workshell/internal/secure"
)

// Player data erasure. Roblox sends a "right to erasure" webhook when a player
// asks for their data to be deleted; the player page has a button for the same
// thing. Both remove everything this project stored about the player.

// erasureIDs is every stored id a Roblox user id may appear as. A project that
// turned hashing on or off part-way has both forms in its data.
func erasureIDs(p *project, robloxID int64) []int64 {
	ids := []int64{robloxID}
	if p.hashSecret != "" {
		if h := secure.HashID([]byte(p.hashSecret), robloxID); h != robloxID {
			ids = append(ids, h)
		}
	}
	return ids
}

// erasePlayer deletes the player's sessions, telemetry, issue links and
// whiteboard snapshots and returns how many rows went. ClickHouse deletions
// complete before the SQLite transaction commits; failures leave session keys
// available for a safe retry.
// Aggregate counters (issue event totals, metrics) stay: they hold no ids.
func (s *Server) erasePlayer(ctx context.Context, projectID int64, ids []int64) (int64, error) {
	if s.telemetry != nil {
		s.telemetryGuard.Lock()
		defer s.telemetryGuard.Unlock()
		if err := s.drainTelemetryLocked(ctx); err != nil {
			return 0, err
		}
	}
	tx, err := s.db.W.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var total int64
	exec := func(query string, args ...any) error {
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("%s: %w", strings.Fields(query)[2], err)
		}
		n, _ := res.RowsAffected()
		total += n
		return nil
	}

	var sessionKeys []string
	for _, id := range ids {
		rows, err := tx.QueryContext(ctx, `SELECT session_key FROM play_sessions WHERE project_id = ? AND user_id = ?`, projectID, id)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var k string
			if rows.Scan(&k) == nil {
				sessionKeys = append(sessionKeys, k)
			}
		}
		rows.Close()
	}

	if s.telemetry != nil {
		eraseRemote := func(table, where string, args ...any) error {
			rows, err := s.telemetry.Query(ctx, `SELECT COUNT(*) FROM `+table+` WHERE `+where, args...)
			if err != nil {
				return err
			}
			var n int64
			if rows.Next() {
				err = rows.Scan(&n)
			}
			readErr := rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			if readErr != nil {
				return readErr
			}
			if n == 0 {
				return nil
			}
			if err := s.telemetry.Delete(ctx, table, where, args...); err != nil {
				return err
			}
			total += n
			return nil
		}
		for _, table := range []string{"logs", "spans", "client_perf"} {
			for _, k := range sessionKeys {
				if err := eraseRemote(table, `project_id = ? AND session_key = ?`, projectID, k); err != nil {
					return 0, err
				}
			}
			if table != "client_perf" {
				for _, id := range ids {
					if err := eraseRemote(table, `project_id = ? AND user_id = ?`, projectID, id); err != nil {
						return 0, err
					}
				}
			}
		}
	}
	// Rows tagged with one of the player's sessions, even where the item itself
	// carried no user id (client perf, spans and logs often don't).
	for _, k := range sessionKeys {
		for _, table := range []string{"logs", "events", "error_events", "spans", "client_perf"} {
			if err := exec(`DELETE FROM `+table+` WHERE project_id = ? AND session_key = ?`, projectID, k); err != nil {
				return 0, err
			}
		}
	}
	for _, id := range ids {
		for _, table := range []string{"logs", "events", "error_events", "spans", "detections", "play_sessions", "players"} {
			if err := exec(`DELETE FROM `+table+` WHERE project_id = ? AND user_id = ?`, projectID, id); err != nil {
				return 0, err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE issues SET user_count = MAX(user_count - 1, 0)
			WHERE project_id = ? AND id IN (SELECT issue_id FROM issue_users WHERE user_id = ?)`, projectID, id); err != nil {
			return 0, err
		}
		if err := exec(`DELETE FROM issue_users WHERE user_id = ? AND issue_id IN (SELECT id FROM issues WHERE project_id = ?)`, id, projectID); err != nil {
			return 0, err
		}
		if err := exec(`
			DELETE FROM issue_activity WHERE kind = 'quarantine' AND json_extract(data, '$.userId') = ?
				AND issue_id IN (SELECT id FROM issues WHERE project_id = ?)`, id, projectID); err != nil {
			return 0, err
		}
	}

	// Whiteboard embeds are snapshots: one that shows this player or one of
	// their sessions is removed from the board.
	n, err := s.eraseBoardSnapshots(ctx, tx, projectID, ids, sessionKeys)
	if err != nil {
		return 0, err
	}
	total += n

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *Server) eraseBoardSnapshots(ctx context.Context, tx *sql.Tx, projectID int64, ids []int64, sessionKeys []string) (int64, error) {
	var alts []string
	for _, id := range ids {
		v := strconv.FormatInt(id, 10)
		alts = append(alts, `/players/`+v+`\b`, `(?i)\bplayer `+v+`\b`)
	}
	for _, k := range sessionKeys {
		alts = append(alts, `/sessions/`+regexp.QuoteMeta(k)+`\b`)
	}
	match := regexp.MustCompile(strings.Join(alts, "|"))

	rows, err := tx.QueryContext(ctx, `
		SELECT i.board_id, i.id, i.data FROM whiteboard_items i JOIN whiteboards b ON b.id = i.board_id
		WHERE b.project_id = ? AND i.kind = 'embed'`, projectID)
	if err != nil {
		return 0, err
	}
	type key struct {
		board int64
		id    string
	}
	var doomed []key
	for rows.Next() {
		var k key
		var data string
		if rows.Scan(&k.board, &k.id, &data) == nil && match.MatchString(data) {
			doomed = append(doomed, k)
		}
	}
	rows.Close()
	for _, k := range doomed {
		if _, err := tx.ExecContext(ctx, `DELETE FROM whiteboard_items WHERE board_id = ? AND id = ?`, k.board, k.id); err != nil {
			return 0, err
		}
	}
	return int64(len(doomed)), nil
}

// handleErasePlayer erases a player from the player page.
func (s *Server) handleErasePlayer(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	raw := r.PathValue("userId")
	var ids []int64
	if r.URL.Query().Get("raw") == "1" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return badRequest("invalid Roblox user id")
		}
		ids = erasureIDs(p, id)
	} else {
		// The page already shows the stored id (hashed or not).
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return badRequest("invalid player id")
		}
		ids = []int64{id}
	}
	n, err := s.erasePlayer(r.Context(), p.ID, ids)
	if err != nil {
		return err
	}
	s.audit(r, p.ID, u, "player.erase", fmt.Sprintf("Manual erasure: %d rows deleted", n))
	return ok(w, map[string]any{"rows": n})
}

// handleRotateRobloxSecret creates the secret Roblox signs webhooks with.
func (s *Server) handleRotateRobloxSecret(w http.ResponseWriter, r *http.Request, p *project, u *user) error {
	secret := "wsr_" + randomHex(24)
	if _, err := s.db.W.ExecContext(r.Context(), `UPDATE projects SET roblox_webhook_secret = ? WHERE id = ?`, s.box.Seal(secret), p.ID); err != nil {
		return err
	}
	s.audit(r, p.ID, u, "roblox_webhook.rotate", "")
	return ok(w, map[string]any{"secret": secret})
}

// robloxWebhookWindow is how old a signed notification may be. Roblox's docs
// suggest a few minutes; erasure is idempotent, so replays inside it are harmless.
const robloxWebhookWindow = 10 * time.Minute

type robloxNotification struct {
	NotificationID string `json:"NotificationId"`
	EventType      string `json:"EventType"`
	EventPayload   struct {
		UserID  int64   `json:"UserId"`
		GameIDs []int64 `json:"GameIds"`
	} `json:"EventPayload"`
}

// handleRobloxWebhook receives Roblox webhook notifications for a project.
// Unsigned requests are refused: without a secret anyone could erase players.
func (s *Server) handleRobloxWebhook(w http.ResponseWriter, r *http.Request) error {
	pid, err := pathInt(r, "pid")
	if err != nil {
		return errNotFound
	}
	var sealed string
	err = s.db.R.QueryRowContext(r.Context(), `SELECT roblox_webhook_secret FROM projects WHERE id = ?`, pid).Scan(&sealed)
	if err != nil || sealed == "" {
		return errNotFound
	}
	secret, err := s.box.Open(sealed)
	if err != nil || secret == "" {
		return errNotFound
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		return badRequest("body too large")
	}
	if !validRobloxSignature(r.Header.Get("Roblox-Signature"), secret, body, time.Now()) {
		return errStatus(http.StatusUnauthorized, "invalid signature")
	}
	var n robloxNotification
	if err := json.Unmarshal(body, &n); err != nil {
		return badRequest("invalid JSON")
	}
	p, err := s.scanProject(s.db.R.QueryRowContext(r.Context(), `SELECT `+projectColumns+` FROM projects WHERE id = ?`, pid))
	if err != nil {
		return err
	}
	switch n.EventType {
	case "SampleNotification":
		s.audit(r, pid, nil, "roblox_webhook.test", "Test notification "+truncate(n.NotificationID, 64))
	case "RightToErasureRequest":
		if n.EventPayload.UserID <= 0 {
			return badRequest("missing UserId")
		}
		// Roblox expects an answer within 5 seconds and retries otherwise.
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		rows, err := s.erasePlayer(ctx, pid, erasureIDs(p, n.EventPayload.UserID))
		if err != nil {
			return err
		}
		// The audit trail records that a request was honoured, never whose.
		s.audit(r, pid, nil, "player.erase", fmt.Sprintf("Roblox erasure request %s: %d rows deleted", truncate(n.NotificationID, 64), rows))
	}
	return ok(w, map[string]any{"ok": true})
}

// validRobloxSignature checks a "t=<unix>,v1=<base64 hmac>" header: the HMAC
// is SHA-256 over "<t>.<body>" keyed with the webhook secret.
func validRobloxSignature(header, secret string, body []byte, now time.Time) bool {
	var ts string
	var sigs [][]byte
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			if b, err := base64.StdEncoding.DecodeString(v); err == nil {
				sigs = append(sigs, b)
			}
		}
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || len(sigs) == 0 {
		return false
	}
	if d := now.Sub(time.Unix(t, 0)); d > robloxWebhookWindow || d < -robloxWebhookWindow {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + "."))
	m.Write(body)
	want := m.Sum(nil)
	for _, sig := range sigs {
		if hmac.Equal(sig, want) {
			return true
		}
	}
	return false
}
