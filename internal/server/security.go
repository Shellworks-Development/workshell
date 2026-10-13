package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

type detectionRow struct {
	ID         int64           `json:"id"`
	TS         int64           `json:"ts"`
	UserID     *int64          `json:"userId"`
	SessionKey string          `json:"sessionKey"`
	JobID      string          `json:"jobId"`
	Kind       string          `json:"kind"`
	Severity   string          `json:"severity"`
	Action     string          `json:"action"`
	Source     string          `json:"source"`
	Details    json.RawMessage `json:"details"`
}

func (s *Server) queryDetections(r *http.Request, where string, args ...any) ([]detectionRow, error) {
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT id, ts, user_id, session_key, job_id, kind, severity, action, source, details
		FROM detections WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []detectionRow{}
	for rows.Next() {
		var d detectionRow
		var details string
		if err := rows.Scan(&d.ID, &d.TS, &d.UserID, &d.SessionKey, &d.JobID, &d.Kind, &d.Severity, &d.Action, &d.Source, &details); err != nil {
			return nil, err
		}
		d.Details = json.RawMessage(details)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Server) handleSecurity(w http.ResponseWriter, r *http.Request, p *project) error {
	ctx := r.Context()
	tr := parseRange(r, "7d")
	var totals struct {
		Detections int64 `json:"detections"`
		Players    int64 `json:"players"`
		High       int64 `json:"high"`
		Automatic  int64 `json:"automatic"`
		Actioned   int64 `json:"actioned"`
	}
	err := s.db.R.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT user_id), COALESCE(SUM(severity = 'high'), 0), COALESCE(SUM(source = 'workshell'), 0),
			COALESCE(SUM(action != '' AND action != 'none'), 0)
		FROM detections WHERE project_id = ? AND ts >= ? AND ts < ?`, p.ID, tr.From, tr.To,
	).Scan(&totals.Detections, &totals.Players, &totals.High, &totals.Automatic, &totals.Actioned)
	if err != nil {
		return err
	}

	series := newTimeseries(tr, "low", "medium", "high")
	rows, err := s.db.R.QueryContext(ctx, `SELECT (ts - ?2) / ?4 * ?4 + ?2, severity, COUNT(*) FROM detections
		WHERE project_id = ?1 AND ts >= ?2 AND ts < ?3 GROUP BY 1, 2`, p.ID, tr.From, tr.To, tr.Bucket)
	if err != nil {
		return err
	}
	for rows.Next() {
		var b, n int64
		var sev string
		if err := rows.Scan(&b, &sev, &n); err != nil {
			rows.Close()
			return err
		}
		series.set(map[string]int{"low": 0, "medium": 1, "high": 2}[sev], b, float64(n))
	}
	rows.Close()

	type kindRow struct {
		Kind     string `json:"kind"`
		Count    int64  `json:"count"`
		Players  int64  `json:"players"`
		High     int64  `json:"high"`
		LastSeen int64  `json:"lastSeen"`
	}
	kinds := []kindRow{}
	rows, err = s.db.R.QueryContext(ctx, `SELECT kind, COUNT(*), COUNT(DISTINCT user_id), SUM(severity = 'high'), MAX(ts) FROM detections
		WHERE project_id = ? AND ts >= ? AND ts < ? GROUP BY kind ORDER BY 2 DESC LIMIT 50`, p.ID, tr.From, tr.To)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k kindRow
		if err := rows.Scan(&k.Kind, &k.Count, &k.Players, &k.High, &k.LastSeen); err != nil {
			rows.Close()
			return err
		}
		kinds = append(kinds, k)
	}
	rows.Close()

	type offender struct {
		UserID   int64    `json:"userId"`
		Count    int64    `json:"count"`
		High     int64    `json:"high"`
		Kinds    []string `json:"kinds"`
		Actions  []string `json:"actions"`
		LastSeen int64    `json:"lastSeen"`
	}
	offenders := []offender{}
	rows, err = s.db.R.QueryContext(ctx, `
		SELECT user_id, COUNT(*), SUM(severity = 'high'), GROUP_CONCAT(DISTINCT kind), COALESCE(GROUP_CONCAT(DISTINCT NULLIF(action, '')), ''), MAX(ts)
		FROM detections WHERE project_id = ? AND ts >= ? AND ts < ? AND user_id IS NOT NULL
		GROUP BY user_id ORDER BY SUM(severity = 'high') DESC, COUNT(*) DESC LIMIT 25`, p.ID, tr.From, tr.To)
	if err != nil {
		return err
	}
	for rows.Next() {
		var o offender
		var kindsCSV, actionsCSV string
		if err := rows.Scan(&o.UserID, &o.Count, &o.High, &kindsCSV, &actionsCSV, &o.LastSeen); err != nil {
			rows.Close()
			return err
		}
		o.Kinds = splitCSV(kindsCSV)
		o.Actions = splitCSV(actionsCSV)
		offenders = append(offenders, o)
	}
	rows.Close()

	recent, err := s.queryDetections(r, `project_id = ? AND ts >= ? ORDER BY ts DESC LIMIT 50`, p.ID, tr.From)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"totals": totals, "series": series, "kinds": kinds, "offenders": offenders, "recent": recent, "hashedIds": p.HashUserIDs})
}

func splitCSV(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, ",")
}

func (s *Server) handleDetections(w http.ResponseWriter, r *http.Request, p *project) error {
	q := r.URL.Query()
	where := []string{"project_id = ?"}
	args := []any{p.ID}
	if v := q.Get("kind"); v != "" {
		where = append(where, "kind = ?")
		args = append(args, v)
	}
	if v := q.Get("severity"); v != "" {
		where = append(where, "severity = ?")
		args = append(args, v)
	}
	if v := q.Get("userId"); v != "" {
		if id, okID := lookupUserID(p, v, q.Get("raw") == "1"); okID {
			where = append(where, "user_id = ?")
			args = append(args, id)
		}
	}
	if before := queryInt(r, "before", 0); before > 0 {
		where = append(where, "id < ?")
		args = append(args, before)
	}
	args = append(args, min(max(queryInt(r, "limit", 100), 1), 500))
	list, err := s.queryDetections(r, strings.Join(where, " AND ")+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return err
	}
	return ok(w, list)
}
