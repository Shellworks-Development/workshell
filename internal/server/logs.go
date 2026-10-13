package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

type logRow struct {
	ID      int64           `json:"id"`
	TS      int64           `json:"ts"`
	Level   string          `json:"level"`
	Message string          `json:"message"`
	JobID   string          `json:"jobId"`
	UserID  *int64          `json:"userId"`
	Source  string          `json:"source"`
	Attrs   json.RawMessage `json:"attrs"`
}

var logLevels = []string{"debug", "info", "warn", "error"}

// handleLogs searches logs, newest first. Pagination: `before=<id>` loads
// older rows; `after=<id>` returns rows newer than id (for live tail).
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request, p *project) error {
	q := r.URL.Query()
	tr := parseRange(r, "24h")

	where := []string{"project_id = ?", "ts >= ?"}
	args := []any{p.ID, tr.From}

	if lv := q.Get("level"); lv != "" {
		var marks []string
		for _, l := range strings.Split(lv, ",") {
			if oneOf(l, logLevels...) == l {
				marks = append(marks, "?")
				args = append(args, l)
			}
		}
		if len(marks) > 0 {
			where = append(where, "level IN ("+strings.Join(marks, ",")+")")
		}
	}
	if search := strings.TrimSpace(q.Get("q")); search != "" {
		where = append(where, "message LIKE ? ESCAPE '\\'")
		args = append(args, "%"+escapeLike(search)+"%")
	}
	if job := q.Get("jobId"); job != "" {
		where = append(where, "job_id = ?")
		args = append(args, job)
	}
	if uid, err := strconv.ParseInt(q.Get("userId"), 10, 64); err == nil {
		where = append(where, "user_id = ?")
		args = append(args, uid)
	}
	filterWhere, filterArgs := strings.Join(where, " AND "), append([]any(nil), args...)

	limit := min(max(queryInt(r, "limit", 100), 1), 500)
	order := "ts DESC, id DESC"
	tail := false
	if after := queryInt(r, "after", 0); after > 0 {
		where = append(where, "id > ?")
		args = append(args, after)
		order = "id ASC"
		tail = true
	} else if before := queryInt(r, "before", 0); before > 0 {
		where = append(where, "id < ?")
		args = append(args, before)
	}
	args = append(args, limit)

	rows, err := s.db.R.QueryContext(r.Context(), `
		SELECT id, ts, level, message, job_id, user_id, source, attrs FROM logs
		WHERE `+strings.Join(where, " AND ")+` ORDER BY `+order+` LIMIT ?`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	logs := []logRow{}
	for rows.Next() {
		var l logRow
		var attrs string
		if err := rows.Scan(&l.ID, &l.TS, &l.Level, &l.Message, &l.JobID, &l.UserID, &l.Source, &attrs); err != nil {
			return err
		}
		l.Attrs = json.RawMessage(attrs)
		logs = append(logs, l)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if tail {
		// Keep the response newest-first like every other page.
		for i, j := 0, len(logs)-1; i < j; i, j = i+1, j-1 {
			logs[i], logs[j] = logs[j], logs[i]
		}
	}

	resp := map[string]any{"logs": logs}
	if q.Get("histogram") == "1" {
		hist := newTimeseries(tr, logLevels...)
		rows, err := s.db.R.QueryContext(r.Context(), `
			SELECT (ts - ?) / ? * ? + ?, level, COUNT(*) FROM logs
			WHERE `+filterWhere+` GROUP BY 1, 2`,
			append([]any{tr.From, tr.Bucket, tr.Bucket, tr.From}, filterArgs...)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var b, n int64
			var level string
			if err := rows.Scan(&b, &level, &n); err != nil {
				return err
			}
			for i, l := range logLevels {
				if l == level {
					hist.set(i, b, float64(n))
				}
			}
		}
		resp["histogram"] = hist
	}
	return ok(w, resp)
}
