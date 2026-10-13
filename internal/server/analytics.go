package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const dayMs = int64(24 * time.Hour / time.Millisecond)

// dayRange is a range of whole UTC days ending today, with daily buckets.
func dayRange(r *http.Request, fallbackDays int) timeRange {
	days := fallbackDays
	switch r.URL.Query().Get("range") {
	case "7d":
		days = 7
	case "14d":
		days = 14
	case "30d":
		days = 30
	case "90d":
		days = 90
	case "180d":
		days = 180
	}
	to := (nowMs()/dayMs + 1) * dayMs
	return timeRange{From: to - int64(days)*dayMs, To: to, Bucket: dayMs, Label: strconv.Itoa(days) + "d"}
}

func (s *Server) handlePlayerAnalytics(w http.ResponseWriter, r *http.Request, p *project) error {
	ctx := r.Context()
	tr := dayRange(r, 30)

	ts := newTimeseries(tr, "active", "new", "returning", "sessions", "avgSessionMin", "playtimeHours", "peakCcu", "revenue", "payers")
	const (
		sActive = iota
		sNew
		sReturning
		sSessions
		sAvgSession
		sPlaytime
		sPeak
		sRevenue
		sPayers
	)

	rows, err := s.db.R.QueryContext(ctx, `
		SELECT (started_at - ?2) / ?4 * ?4 + ?2, COUNT(DISTINCT user_id), COUNT(DISTINCT CASE WHEN is_new THEN user_id END), COUNT(*),
			COALESCE(AVG(ended_at - started_at), 0), COALESCE(SUM(ended_at - started_at), 0)
		FROM play_sessions WHERE project_id = ?1 AND started_at >= ?2 AND started_at < ?3 GROUP BY 1`,
		p.ID, tr.From, tr.To, tr.Bucket)
	if err != nil {
		return err
	}
	for rows.Next() {
		var b int64
		var active, nw, sessions, avg, total float64
		if err := rows.Scan(&b, &active, &nw, &sessions, &avg, &total); err != nil {
			rows.Close()
			return err
		}
		ts.set(sActive, b, active)
		ts.set(sNew, b, nw)
		ts.set(sReturning, b, active-nw)
		ts.set(sSessions, b, sessions)
		ts.set(sAvgSession, b, avg/60000)
		ts.set(sPlaytime, b, total/3600000)
	}
	rows.Close()

	rows, err = s.db.R.QueryContext(ctx, `
		SELECT (ts - ?2) / ?4 * ?4 + ?2, COALESCE(SUM(value), 0), COUNT(DISTINCT user_id)
		FROM events WHERE project_id = ?1 AND name = '$purchase' AND ts >= ?2 AND ts < ?3 GROUP BY 1`,
		p.ID, tr.From, tr.To, tr.Bucket)
	if err != nil {
		return err
	}
	for rows.Next() {
		var b int64
		var rev, payers float64
		if err := rows.Scan(&b, &rev, &payers); err != nil {
			rows.Close()
			return err
		}
		ts.set(sRevenue, b, rev)
		ts.set(sPayers, b, payers)
	}
	rows.Close()

	// Peak CCU per day = max of the hourly concurrent-player series.
	hourly, err := s.ccuSeries(r, p.ID, timeRange{From: tr.From, To: tr.To, Bucket: int64(time.Hour / time.Millisecond)}, "")
	if err != nil {
		return err
	}
	for i, pt := range hourly.Points {
		idx := (pt - tr.From) / tr.Bucket
		if v := hourly.Series[0].Values[i]; v > ts.Series[sPeak].Values[idx] {
			ts.Series[sPeak].Values[idx] = v
		}
	}

	var summary struct {
		UniquePlayers int64   `json:"uniquePlayers"`
		NewPlayers    int64   `json:"newPlayers"`
		Sessions      int64   `json:"sessions"`
		AvgSessionMs  float64 `json:"avgSessionMs"`
		PlaytimeMs    float64 `json:"playtimeMs"`
		Revenue       float64 `json:"revenue"`
		Payers        int64   `json:"payers"`
		TotalPlayers  int64   `json:"totalPlayers"`
	}
	err = s.db.R.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(DISTINCT user_id) FROM play_sessions WHERE project_id = ?1 AND started_at >= ?2 AND started_at < ?3),
			(SELECT COUNT(*) FROM players WHERE project_id = ?1 AND first_seen >= ?2 AND first_seen < ?3),
			(SELECT COUNT(*) FROM play_sessions WHERE project_id = ?1 AND started_at >= ?2 AND started_at < ?3),
			(SELECT COALESCE(AVG(ended_at - started_at), 0) FROM play_sessions WHERE project_id = ?1 AND started_at >= ?2 AND started_at < ?3 AND ended_at IS NOT NULL),
			(SELECT COALESCE(SUM(ended_at - started_at), 0) FROM play_sessions WHERE project_id = ?1 AND started_at >= ?2 AND started_at < ?3 AND ended_at IS NOT NULL),
			(SELECT COALESCE(SUM(value), 0) FROM events WHERE project_id = ?1 AND name = '$purchase' AND ts >= ?2 AND ts < ?3),
			(SELECT COUNT(DISTINCT user_id) FROM events WHERE project_id = ?1 AND name = '$purchase' AND ts >= ?2 AND ts < ?3),
			(SELECT COUNT(*) FROM players WHERE project_id = ?1)`,
		p.ID, tr.From, tr.To,
	).Scan(&summary.UniquePlayers, &summary.NewPlayers, &summary.Sessions, &summary.AvgSessionMs,
		&summary.PlaytimeMs, &summary.Revenue, &summary.Payers, &summary.TotalPlayers)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"series": ts, "summary": summary})
}

var retentionOffsets = []int64{1, 2, 3, 4, 5, 6, 7, 14, 30}

// handleRetention returns day-N retention for daily cohorts of new players:
// the share of a day's new players who started a session N days later.
func (s *Server) handleRetention(w http.ResponseWriter, r *http.Request, p *project) error {
	tr := dayRange(r, 14)
	type cohort struct {
		Day      int64      `json:"day"`
		Size     int64      `json:"size"`
		Retained []*float64 `json:"retained"` // nil = not enough time has passed
	}
	today := nowMs() / dayMs
	cohorts := map[int64]*cohort{}
	var order []int64

	rows, err := s.db.R.QueryContext(r.Context(), `
		SELECT first_seen / ?4, COUNT(*) FROM players
		WHERE project_id = ?1 AND first_seen >= ?2 AND first_seen < ?3 GROUP BY 1 ORDER BY 1`,
		p.ID, tr.From, tr.To, dayMs)
	if err != nil {
		return err
	}
	for rows.Next() {
		c := &cohort{Retained: make([]*float64, len(retentionOffsets))}
		if err := rows.Scan(&c.Day, &c.Size); err != nil {
			rows.Close()
			return err
		}
		for i, off := range retentionOffsets {
			if c.Day+off <= today {
				zero := 0.0
				c.Retained[i] = &zero
			}
		}
		cohorts[c.Day] = c
		order = append(order, c.Day)
	}
	rows.Close()

	rows, err = s.db.R.QueryContext(r.Context(), `
		WITH cohort AS (
			SELECT user_id, first_seen / ?4 AS d0 FROM players
			WHERE project_id = ?1 AND first_seen >= ?2 AND first_seen < ?3
		), activity AS (
			SELECT DISTINCT user_id, started_at / ?4 AS d FROM play_sessions
			WHERE project_id = ?1 AND started_at >= ?2
		)
		SELECT c.d0, a.d - c.d0, COUNT(*) FROM cohort c JOIN activity a ON a.user_id = c.user_id
		WHERE a.d > c.d0 AND a.d - c.d0 <= 30
		GROUP BY 1, 2`,
		p.ID, tr.From, tr.To, dayMs)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var d0, off, n int64
		if err := rows.Scan(&d0, &off, &n); err != nil {
			return err
		}
		c := cohorts[d0]
		if c == nil || c.Size == 0 {
			continue
		}
		if i := slices.Index(retentionOffsets, off); i >= 0 && c.Retained[i] != nil {
			v := float64(n) / float64(c.Size)
			c.Retained[i] = &v
		}
	}

	out := make([]*cohort, 0, len(order))
	for i := len(order) - 1; i >= 0; i-- {
		c := cohorts[order[i]]
		c.Day *= dayMs
		out = append(out, c)
	}
	return ok(w, map[string]any{"offsets": retentionOffsets, "cohorts": out})
}

// handleBreakdown groups sessions in the range by platform or country.
func (s *Server) handleBreakdown(w http.ResponseWriter, r *http.Request, p *project) error {
	tr := dayRange(r, 30)
	col := map[string]string{"platform": "platform", "country": "country"}[r.URL.Query().Get("by")]
	if col == "" {
		return badRequest("by must be platform or country")
	}
	rows, err := s.db.R.QueryContext(r.Context(), `
		SELECT `+col+`, COUNT(DISTINCT user_id), COUNT(*), COALESCE(AVG(ended_at - started_at), 0)
		FROM play_sessions WHERE project_id = ? AND started_at >= ? AND started_at < ?
		GROUP BY 1 ORDER BY 2 DESC LIMIT 50`, p.ID, tr.From, tr.To)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct {
		Value        string  `json:"value"`
		Players      int64   `json:"players"`
		Sessions     int64   `json:"sessions"`
		AvgSessionMs float64 `json:"avgSessionMs"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.Value, &x.Players, &x.Sessions, &x.AvgSessionMs); err != nil {
			return err
		}
		if x.Value == "" {
			x.Value = "Unknown"
		}
		out = append(out, x)
	}
	return ok(w, out)
}

// ---- Custom events ----------------------------------------------------------

type eventRow struct {
	ID     int64           `json:"id"`
	TS     int64           `json:"ts"`
	Name   string          `json:"name"`
	UserID *int64          `json:"userId"`
	JobID  string          `json:"jobId"`
	Value  *float64        `json:"value"`
	Props  json.RawMessage `json:"props"`
}

func (s *Server) queryEvents(r *http.Request, query string, args ...any) ([]eventRow, error) {
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT id, ts, name, user_id, job_id, value, props FROM events `+query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []eventRow{}
	for rows.Next() {
		var e eventRow
		var props string
		if err := rows.Scan(&e.ID, &e.TS, &e.Name, &e.UserID, &e.JobID, &e.Value, &props); err != nil {
			return nil, err
		}
		e.Props = json.RawMessage(props)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Server) handleListEventNames(w http.ResponseWriter, r *http.Request, p *project) error {
	tr := parseRange(r, "7d")
	rows, err := s.db.R.QueryContext(r.Context(), `
		SELECT name, COUNT(*), COUNT(DISTINCT user_id), COALESCE(SUM(value), 0), MAX(ts)
		FROM events WHERE project_id = ? AND ts >= ? AND ts < ?
		GROUP BY name ORDER BY 2 DESC LIMIT 500`, p.ID, tr.From, tr.To)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct {
		Name     string  `json:"name"`
		Count    int64   `json:"count"`
		Users    int64   `json:"users"`
		ValueSum float64 `json:"valueSum"`
		LastSeen int64   `json:"lastSeen"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.Name, &x.Count, &x.Users, &x.ValueSum, &x.LastSeen); err != nil {
			return err
		}
		out = append(out, x)
	}
	return ok(w, out)
}

var propKeyPattern = func(k string) bool {
	if k == "" || len(k) > 64 {
		return false
	}
	for _, c := range k {
		if !(c == '_' || c == '-' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// handleEventSeries charts one event over time. measure = count | users | sum | avg.
// With groupBy=<prop>, it returns one series per top value of that property.
func (s *Server) handleEventSeries(w http.ResponseWriter, r *http.Request, p *project) error {
	q := r.URL.Query()
	tr := parseRange(r, "7d")
	name := q.Get("name")
	if name == "" {
		return badRequest("name is required")
	}
	agg := map[string]string{
		"count": "COUNT(*)",
		"users": "COUNT(DISTINCT user_id)",
		"sum":   "COALESCE(SUM(value), 0)",
		"avg":   "COALESCE(AVG(value), 0)",
	}[q.Get("measure")]
	if agg == "" {
		agg = "COUNT(*)"
	}

	groupBy := q.Get("groupBy")
	groupExpr := "''"
	var groupArgs []any
	if groupBy != "" {
		if !propKeyPattern(groupBy) {
			return badRequest("invalid property name")
		}
		groupExpr = "COALESCE(CAST(json_extract(props, ?) AS TEXT), '(none)')"
		groupArgs = []any{"$." + groupBy}
	}

	// Pick the top groups over the whole range first, then chart only those.
	var groups []string
	breakdown := []map[string]any{}
	if groupBy != "" {
		args := append(append([]any{}, groupArgs...), p.ID, name, tr.From, tr.To)
		rows, err := s.db.R.QueryContext(r.Context(), `
			SELECT `+groupExpr+` AS g, `+agg+` FROM events
			WHERE project_id = ? AND name = ? AND ts >= ? AND ts < ?
			GROUP BY g ORDER BY 2 DESC LIMIT 20`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var g string
			var v float64
			if err := rows.Scan(&g, &v); err != nil {
				rows.Close()
				return err
			}
			if len(groups) < 8 {
				groups = append(groups, g)
			}
			breakdown = append(breakdown, map[string]any{"value": g, "total": v})
		}
		rows.Close()
	} else {
		groups = []string{name}
	}

	ts := newTimeseries(tr, groups...)
	args := append([]any{tr.From, tr.Bucket, tr.Bucket, tr.From}, groupArgs...)
	args = append(args, p.ID, name, tr.From, tr.To)
	rows, err := s.db.R.QueryContext(r.Context(), `
		SELECT (ts - ?) / ? * ? + ? AS b, `+groupExpr+` AS g, `+agg+` FROM events
		WHERE project_id = ? AND name = ? AND ts >= ? AND ts < ?
		GROUP BY b, g`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var b int64
		var g string
		var v float64
		if err := rows.Scan(&b, &g, &v); err != nil {
			return err
		}
		if groupBy == "" {
			ts.set(0, b, v)
		} else if i := slices.Index(groups, g); i >= 0 {
			ts.set(i, b, v)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	// Property keys seen on recent events, for the group-by picker.
	keys := map[string]bool{}
	recent, err := s.queryEvents(r, `WHERE project_id = ? AND name = ? ORDER BY ts DESC LIMIT 200`, p.ID, name)
	if err != nil {
		return err
	}
	for _, e := range recent {
		var m map[string]any
		if json.Unmarshal(e.Props, &m) == nil {
			for k := range m {
				keys[k] = true
			}
		}
	}
	propKeys := make([]string, 0, len(keys))
	for k := range keys {
		propKeys = append(propKeys, k)
	}
	slices.Sort(propKeys)

	return ok(w, map[string]any{"series": ts, "breakdown": breakdown, "propKeys": propKeys})
}

func (s *Server) handleRecentEvents(w http.ResponseWriter, r *http.Request, p *project) error {
	name := r.URL.Query().Get("name")
	before := queryInt(r, "before", 1<<62)
	limit := min(max(queryInt(r, "limit", 100), 1), 500)
	events, err := s.queryEvents(r, `WHERE project_id = ? AND (? = '' OR name = ?) AND id < ? ORDER BY ts DESC, id DESC LIMIT ?`,
		p.ID, name, name, before, limit)
	if err != nil {
		return err
	}
	return ok(w, events)
}

// handleFunnel computes an ordered conversion funnel: for each step, how many
// players performed every previous step in order (each step within `windowHours`
// of the first) during the range. "$session" may be used as a step for joining.
func (s *Server) handleFunnel(w http.ResponseWriter, r *http.Request, p *project) error {
	var body struct {
		Steps       []string `json:"steps"`
		Range       string   `json:"range"`
		WindowHours int64    `json:"windowHours"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if len(body.Steps) < 2 || len(body.Steps) > 10 {
		return badRequest("a funnel needs 2-10 steps")
	}
	if body.WindowHours <= 0 {
		body.WindowHours = 24
	}
	window := body.WindowHours * 3600_000
	preset, found := rangePresets[body.Range]
	if !found {
		preset = rangePresets["7d"]
	}
	from := nowMs() - preset.span.Milliseconds()

	placeholders := make([]string, len(body.Steps))
	args := []any{p.ID, from}
	for i, st := range body.Steps {
		placeholders[i] = "?"
		args = append(args, st)
	}
	query := `
		SELECT user_id, name, ts, 1 FROM events
		WHERE project_id = ? AND ts >= ? AND user_id IS NOT NULL AND name IN (` + strings.Join(placeholders, ",") + `)`
	if slices.Contains(body.Steps, "$session") {
		query += ` UNION ALL SELECT user_id, '$session', started_at, 0 FROM play_sessions WHERE project_id = ? AND started_at >= ?`
		args = append(args, p.ID, from)
	}
	// Joins sort before same-millisecond events.
	query += ` ORDER BY 1, 3, 4`

	rows, err := s.db.R.QueryContext(r.Context(), query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	// Rows arrive grouped by user in time order. Each user's best progress
	// across attempts counts; an attempt restarts on a first-step event once
	// the window since the attempt began has elapsed.
	counts := make([]int64, len(body.Steps))
	var curUser int64 = -1
	var step, best int
	var start int64
	flush := func() {
		for i := 0; i < best; i++ {
			counts[i]++
		}
	}
	for rows.Next() {
		var uid, ts, order int64
		var name string
		if err := rows.Scan(&uid, &name, &ts, &order); err != nil {
			return err
		}
		if uid != curUser {
			if curUser != -1 {
				flush()
			}
			curUser, step, best, start = uid, 0, 0, 0
		}
		if best == len(body.Steps) {
			continue
		}
		if step > 0 && ts-start > window {
			step = 0
		}
		if name == body.Steps[step] {
			if step == 0 {
				start = ts
			}
			step++
			best = max(best, step)
		}
	}
	if curUser != -1 {
		flush()
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return ok(w, map[string]any{"steps": body.Steps, "counts": counts})
}
