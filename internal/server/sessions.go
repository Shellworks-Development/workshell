package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/workshell/workshell/internal/secure"
)

type sessionRow struct {
	Key        string          `json:"key"`
	UserID     int64           `json:"userId"`
	JobID      string          `json:"jobId"`
	StartedAt  int64           `json:"startedAt"`
	EndedAt    *int64          `json:"endedAt"`
	IsNew      bool            `json:"isNew"`
	Platform   string          `json:"platform"`
	Country    string          `json:"country"`
	Release    string          `json:"release"`
	ErrorCount int64           `json:"errorCount"`
	Attrs      json.RawMessage `json:"attrs"`
}

const sessionColumns = `session_key, user_id, job_id, started_at, ended_at, is_new, platform, country, release, error_count, attrs`

func scanSession(row interface{ Scan(...any) error }) (*sessionRow, error) {
	var x sessionRow
	var attrs string
	if err := row.Scan(&x.Key, &x.UserID, &x.JobID, &x.StartedAt, &x.EndedAt, &x.IsNew, &x.Platform, &x.Country, &x.Release, &x.ErrorCount, &attrs); err != nil {
		return nil, err
	}
	x.Attrs = json.RawMessage(attrs)
	return &x, nil
}

// lookupUserID turns a user id from the dashboard into the stored id: when the
// project pseudonymizes players, a raw Roblox id is hashed the same way.
func lookupUserID(p *project, raw string, isRobloxID bool) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	if isRobloxID && p.HashUserIDs && p.hashSecret != "" {
		return secure.HashID([]byte(p.hashSecret), id), true
	}
	return id, true
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request, p *project) error {
	q := r.URL.Query()
	tr := parseRange(r, "24h")
	where := []string{"project_id = ?"}
	args := []any{p.ID}
	live := q.Get("state") == "live"
	if live {
		where = append(where, "ended_at IS NULL", "job_id IN (SELECT job_id FROM game_servers WHERE project_id = ? AND closed_at IS NULL AND last_seen > ?)")
		args = append(args, p.ID, nowMs()-liveWindow.Milliseconds())
	} else {
		where = append(where, "started_at >= ?")
		args = append(args, tr.From)
	}
	if v := q.Get("userId"); v != "" {
		if id, okID := lookupUserID(p, v, q.Get("raw") == "1"); okID {
			where = append(where, "user_id = ?")
			args = append(args, id)
		}
	}
	if v := strings.TrimSpace(q.Get("q")); v != "" {
		where = append(where, "session_key LIKE ? ESCAPE '\\'")
		args = append(args, escapeLike(v)+"%")
	}
	for _, f := range []string{"release", "platform", "country"} {
		if v := q.Get(f); v != "" {
			where = append(where, f+" = ?")
			args = append(args, v)
		}
	}
	if q.Get("errors") == "1" {
		where = append(where, "error_count > 0")
	}
	limit := min(max(queryInt(r, "limit", 100), 1), 500)
	args = append(args, limit)
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT `+sessionColumns+` FROM play_sessions WHERE `+strings.Join(where, " AND ")+`
		ORDER BY started_at DESC LIMIT ?`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []*sessionRow{}
	for rows.Next() {
		x, err := scanSession(rows)
		if err != nil {
			return err
		}
		out = append(out, x)
	}
	return ok(w, map[string]any{"sessions": out, "hashedIds": p.HashUserIDs})
}

type timelineItem struct {
	TS     int64  `json:"ts"`
	Kind   string `json:"kind"` // join | leave | event | log | error | span | detection | purchase
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Level  string `json:"level,omitempty"`
	Data   any    `json:"data,omitempty"`
}

// timeline merges everything recorded for one session (or, with an empty
// session key, a player's activity in a time window) in chronological order.
func (s *Server) timeline(ctx context.Context, p *project, sessionKey string, userID, from, to int64, limit int) ([]timelineItem, error) {
	var items []timelineItem
	// Rows tagged with the session id, plus untagged rows from the same player
	// in the session's time window. Two indexed queries rather than an OR.
	args := []any{p.ID, sessionKey, userID, from, to}
	q := func(cols, table, tsCol string) (*sql.Rows, error) {
		return s.db.R.QueryContext(ctx, `SELECT * FROM (
			SELECT `+cols+` FROM `+table+` WHERE project_id = ?1 AND ?2 != '' AND session_key = ?2
			UNION ALL
			SELECT `+cols+` FROM `+table+` WHERE project_id = ?1 AND user_id = ?3 AND `+tsCol+` BETWEEN ?4 AND ?5 AND (?2 = '' OR session_key = '')
		) ORDER BY 1 DESC LIMIT `+strconv.Itoa(limit), args...)
	}

	rows, err := q(`ts, name, value, props`, "events", "ts")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ts int64
		var name, props string
		var value *float64
		if err := rows.Scan(&ts, &name, &value, &props); err != nil {
			rows.Close()
			return nil, err
		}
		kind := "event"
		if name == "$purchase" {
			kind = "purchase"
		}
		items = append(items, timelineItem{TS: ts, Kind: kind, Title: name, Data: map[string]any{"value": value, "props": json.RawMessage(props)}})
	}
	rows.Close()

	rows, err = q(`ts, level, message, source, attrs`, "logs", "ts")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ts int64
		var level, msg, source, attrs string
		if err := rows.Scan(&ts, &level, &msg, &source, &attrs); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, timelineItem{TS: ts, Kind: "log", Title: truncate(msg, 500), Level: level, Data: map[string]any{"source": source, "attrs": json.RawMessage(attrs)}})
	}
	rows.Close()

	rows, err = q(`ts, id, issue_id, message, side, breadcrumbs`, "error_events", "ts")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, ts, issue int64
		var msg, side, crumbs string
		if err := rows.Scan(&ts, &id, &issue, &msg, &side, &crumbs); err != nil {
			rows.Close()
			return nil, err
		}
		_, text := splitMessage(msg)
		items = append(items, timelineItem{TS: ts, Kind: "error", Title: truncate(text, 300), Level: "error",
			Data: map[string]any{"issueId": issue, "side": side, "message": msg, "breadcrumbs": json.RawMessage(crumbs)}})
	}
	rows.Close()

	rows, err = q(`ts, name, duration_ms, status`, "spans", "ts")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ts int64
		var name, status string
		var dur float64
		if err := rows.Scan(&ts, &name, &dur, &status); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, timelineItem{TS: ts, Kind: "span", Title: name, Data: map[string]any{"durationMs": dur, "status": status}})
	}
	rows.Close()

	rows, err = q(`ts, kind, severity, action, source, details`, "detections", "ts")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ts int64
		var kind, severity, action, source, details string
		if err := rows.Scan(&ts, &kind, &severity, &action, &source, &details); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, timelineItem{TS: ts, Kind: "detection", Title: kind, Level: severity,
			Data: map[string]any{"action": action, "source": source, "details": json.RawMessage(details)}})
	}
	rows.Close()

	sort.SliceStable(items, func(i, j int) bool { return items[i].TS < items[j].TS })
	if len(items) > limit {
		items = items[len(items)-limit:]
	}
	return items, nil
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request, p *project) error {
	ss, err := scanSession(s.db.R.QueryRowContext(r.Context(), `SELECT `+sessionColumns+` FROM play_sessions WHERE project_id = ? AND session_key = ?`, p.ID, r.PathValue("sid")))
	if errors.Is(err, sql.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	end := nowMs()
	if ss.EndedAt != nil {
		end = *ss.EndedAt
	}
	items, err := s.timeline(r.Context(), p, ss.Key, ss.UserID, ss.StartedAt, end+1000, 2000)
	if err != nil {
		return err
	}
	items = append([]timelineItem{{TS: ss.StartedAt, Kind: "join", Title: "Joined", Detail: ss.JobID}}, items...)
	if ss.EndedAt != nil {
		items = append(items, timelineItem{TS: *ss.EndedAt, Kind: "leave", Title: "Left"})
	}

	// Client performance samples for the session chart.
	type perf struct {
		TS       int64   `json:"ts"`
		FPS      float64 `json:"fps"`
		MemoryMb float64 `json:"memoryMb"`
		PingMs   float64 `json:"pingMs"`
	}
	perfs := []perf{}
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT ts, fps, memory_mb, ping_ms FROM client_perf WHERE project_id = ? AND session_key = ? ORDER BY ts LIMIT 2000`, p.ID, ss.Key)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var x perf
		if err := rows.Scan(&x.TS, &x.FPS, &x.MemoryMb, &x.PingMs); err != nil {
			return err
		}
		perfs = append(perfs, x)
	}
	return ok(w, map[string]any{"session": ss, "timeline": items, "perf": perfs, "hashedIds": p.HashUserIDs})
}

func (s *Server) handleGetPlayer(w http.ResponseWriter, r *http.Request, p *project) error {
	userID, okID := lookupUserID(p, r.PathValue("userId"), r.URL.Query().Get("raw") == "1")
	if !okID {
		return errNotFound
	}
	var pl struct {
		UserID       int64   `json:"userId"`
		FirstSeen    int64   `json:"firstSeen"`
		LastSeen     int64   `json:"lastSeen"`
		SessionCount int64   `json:"sessionCount"`
		PlaytimeMs   int64   `json:"playtimeMs"`
		Spent        float64 `json:"spent"`
		Detections   int64   `json:"detections"`
		Online       bool    `json:"online"`
	}
	err := s.db.R.QueryRowContext(r.Context(), `
		SELECT user_id, first_seen, last_seen, session_count, playtime_ms,
			(SELECT COALESCE(SUM(value), 0) FROM events WHERE project_id = ?1 AND user_id = ?2 AND name = '$purchase'),
			(SELECT COUNT(*) FROM detections WHERE project_id = ?1 AND user_id = ?2),
			EXISTS (SELECT 1 FROM play_sessions WHERE project_id = ?1 AND user_id = ?2 AND ended_at IS NULL)
		FROM players WHERE project_id = ?1 AND user_id = ?2`, p.ID, userID,
	).Scan(&pl.UserID, &pl.FirstSeen, &pl.LastSeen, &pl.SessionCount, &pl.PlaytimeMs, &pl.Spent, &pl.Detections, &pl.Online)
	if errors.Is(err, sql.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return err
	}

	sessions := []*sessionRow{}
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT `+sessionColumns+` FROM play_sessions WHERE project_id = ? AND user_id = ? ORDER BY started_at DESC LIMIT 50`, p.ID, userID)
	if err != nil {
		return err
	}
	for rows.Next() {
		x, err := scanSession(rows)
		if err != nil {
			rows.Close()
			return err
		}
		sessions = append(sessions, x)
	}
	rows.Close()

	issues := []map[string]any{}
	rows, err = s.db.R.QueryContext(r.Context(), `
		SELECT i.id, i.title, i.culprit, i.status, i.last_seen FROM issue_users u JOIN issues i ON i.id = u.issue_id
		WHERE u.user_id = ? AND i.project_id = ? ORDER BY i.last_seen DESC LIMIT 25`, userID, p.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, last int64
		var title, culprit, status string
		if err := rows.Scan(&id, &title, &culprit, &status, &last); err != nil {
			rows.Close()
			return err
		}
		issues = append(issues, map[string]any{"id": id, "title": title, "culprit": culprit, "status": status, "lastSeen": last})
	}
	rows.Close()

	recent, err := s.timeline(r.Context(), p, "", userID, pl.LastSeen-7*dayMs, nowMs(), 300)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"player": pl, "sessions": sessions, "issues": issues, "timeline": recent, "hashedIds": p.HashUserIDs})
}

func (s *Server) handleListPlayers(w http.ResponseWriter, r *http.Request, p *project) error {
	q := r.URL.Query()
	where := []string{"pl.project_id = ?"}
	args := []any{p.ID}
	if v := q.Get("q"); v != "" {
		if id, okID := lookupUserID(p, v, q.Get("raw") == "1"); okID {
			where = append(where, "pl.user_id = ?")
			args = append(args, id)
		} else {
			return ok(w, map[string]any{"players": []any{}, "hashedIds": p.HashUserIDs})
		}
	}
	order := "pl.last_seen DESC"
	switch q.Get("sort") {
	case "playtime":
		order = "pl.playtime_ms DESC"
	case "sessions":
		order = "pl.session_count DESC"
	case "new":
		order = "pl.first_seen DESC"
	}
	args = append(args, min(max(queryInt(r, "limit", 100), 1), 500))
	rows, err := s.db.R.QueryContext(r.Context(), `
		SELECT pl.user_id, pl.first_seen, pl.last_seen, pl.session_count, pl.playtime_ms,
			EXISTS (SELECT 1 FROM play_sessions ps WHERE ps.project_id = pl.project_id AND ps.user_id = pl.user_id AND ps.ended_at IS NULL),
			(SELECT COUNT(*) FROM detections d WHERE d.project_id = pl.project_id AND d.user_id = pl.user_id)
		FROM players pl WHERE `+strings.Join(where, " AND ")+` ORDER BY `+order+` LIMIT ?`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct {
		UserID       int64 `json:"userId"`
		FirstSeen    int64 `json:"firstSeen"`
		LastSeen     int64 `json:"lastSeen"`
		SessionCount int64 `json:"sessionCount"`
		PlaytimeMs   int64 `json:"playtimeMs"`
		Online       bool  `json:"online"`
		Detections   int64 `json:"detections"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.UserID, &x.FirstSeen, &x.LastSeen, &x.SessionCount, &x.PlaytimeMs, &x.Online, &x.Detections); err != nil {
			return err
		}
		out = append(out, x)
	}
	return ok(w, map[string]any{"players": out, "hashedIds": p.HashUserIDs})
}
