package server

import (
	"math"
	"net/http"
	"sort"
)

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[min(max(idx, 0), len(sorted)-1)]
}

const maxSpanSample = 200_000

// handlePerformance summarizes traced operations (spans) and client
// performance for a time range.
func (s *Server) handlePerformance(w http.ResponseWriter, r *http.Request, p *project) error {
	ctx := r.Context()
	tr := parseRange(r, "24h")

	type txn struct {
		Name      string  `json:"name"`
		Count     int64   `json:"count"`
		Errors    int64   `json:"errors"`
		AvgMs     float64 `json:"avgMs"`
		P50       float64 `json:"p50"`
		P95       float64 `json:"p95"`
		P99       float64 `json:"p99"`
		PerMinute float64 `json:"perMinute"`
	}
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT name, COUNT(*), SUM(status = 'error'), AVG(duration_ms) FROM spans
		WHERE project_id = ? AND ts >= ? AND ts < ? GROUP BY name ORDER BY COUNT(*) DESC LIMIT 100`, p.ID, tr.From, tr.To)
	if err != nil {
		return err
	}
	txns := []*txn{}
	for rows.Next() {
		x := &txn{}
		if err := rows.Scan(&x.Name, &x.Count, &x.Errors, &x.AvgMs); err != nil {
			rows.Close()
			return err
		}
		x.PerMinute = float64(x.Count) / (float64(tr.To-tr.From) / 60_000)
		txns = append(txns, x)
	}
	rows.Close()
	for _, x := range txns {
		d, err := s.spanDurations(r, p.ID, x.Name, tr.From, tr.To)
		if err != nil {
			return err
		}
		x.P50, x.P95, x.P99 = percentile(d, 0.5), percentile(d, 0.95), percentile(d, 0.99)
	}

	// Client performance by platform.
	type platformPerf struct {
		Platform string  `json:"platform"`
		Samples  int64   `json:"samples"`
		FPS      float64 `json:"fps"`
		PingMs   float64 `json:"pingMs"`
		MemoryMb float64 `json:"memoryMb"`
		LowFPS   float64 `json:"lowFpsShare"` // share of samples under 30 FPS
	}
	platforms := []platformPerf{}
	rows, err = s.db.R.QueryContext(ctx, `
		SELECT CASE WHEN platform = '' THEN 'Unknown' ELSE platform END, COUNT(*), AVG(fps), AVG(ping_ms), AVG(memory_mb), AVG(fps < 30)
		FROM client_perf WHERE project_id = ? AND ts >= ? AND ts < ? GROUP BY 1 ORDER BY 2 DESC`, p.ID, tr.From, tr.To)
	if err != nil {
		return err
	}
	for rows.Next() {
		var x platformPerf
		if err := rows.Scan(&x.Platform, &x.Samples, &x.FPS, &x.PingMs, &x.MemoryMb, &x.LowFPS); err != nil {
			rows.Close()
			return err
		}
		platforms = append(platforms, x)
	}
	rows.Close()

	client := newTimeseries(tr, "fps", "pingMs", "memoryMb")
	rows, err = s.db.R.QueryContext(ctx, `
		SELECT (ts - ?2) / ?4 * ?4 + ?2, AVG(fps), AVG(ping_ms), AVG(memory_mb) FROM client_perf
		WHERE project_id = ?1 AND ts >= ?2 AND ts < ?3 GROUP BY 1`, p.ID, tr.From, tr.To, tr.Bucket)
	if err != nil {
		return err
	}
	for rows.Next() {
		var b int64
		var fps, ping, mem float64
		if err := rows.Scan(&b, &fps, &ping, &mem); err != nil {
			rows.Close()
			return err
		}
		client.set(0, b, fps)
		client.set(1, b, ping)
		client.set(2, b, mem)
	}
	rows.Close()

	server, err := s.serverSeries(ctx, p.ID, tr, "")
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"transactions": txns, "platforms": platforms, "client": client, "server": server})
}

func (s *Server) spanDurations(r *http.Request, pid int64, name string, from, to int64) ([]float64, error) {
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT duration_ms FROM spans WHERE project_id = ? AND name = ? AND ts >= ? AND ts < ? LIMIT ?`,
		pid, name, from, to, maxSpanSample)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var d []float64
	for rows.Next() {
		var v float64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		d = append(d, v)
	}
	sort.Float64s(d)
	return d, rows.Err()
}

func (s *Server) handleSpanDetail(w http.ResponseWriter, r *http.Request, p *project) error {
	name := r.URL.Query().Get("name")
	if name == "" {
		return badRequest("name is required")
	}
	tr := parseRange(r, "24h")
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT ts, duration_ms, status FROM spans WHERE project_id = ? AND name = ? AND ts >= ? AND ts < ? LIMIT ?`,
		p.ID, name, tr.From, tr.To, maxSpanSample)
	if err != nil {
		return err
	}
	buckets := map[int64][]float64{}
	errs := map[int64]float64{}
	for rows.Next() {
		var ts int64
		var d float64
		var status string
		if err := rows.Scan(&ts, &d, &status); err != nil {
			rows.Close()
			return err
		}
		b := (ts-tr.From)/tr.Bucket*tr.Bucket + tr.From
		buckets[b] = append(buckets[b], d)
		if status == "error" {
			errs[b]++
		}
	}
	rows.Close()
	ts := newTimeseries(tr, "p50", "p95", "count", "errors")
	for b, d := range buckets {
		sort.Float64s(d)
		ts.set(0, b, percentile(d, 0.5))
		ts.set(1, b, percentile(d, 0.95))
		ts.set(2, b, float64(len(d)))
		ts.set(3, b, errs[b])
	}

	type slow struct {
		TS         int64   `json:"ts"`
		DurationMs float64 `json:"durationMs"`
		Status     string  `json:"status"`
		SessionKey string  `json:"sessionKey"`
		UserID     *int64  `json:"userId"`
		JobID      string  `json:"jobId"`
		Release    string  `json:"release"`
		Attrs      string  `json:"attrs"`
	}
	slowest := []slow{}
	rows, err = s.db.R.QueryContext(r.Context(), `SELECT ts, duration_ms, status, session_key, user_id, job_id, release, attrs FROM spans
		WHERE project_id = ? AND name = ? AND ts >= ? AND ts < ? ORDER BY duration_ms DESC LIMIT 20`, p.ID, name, tr.From, tr.To)
	if err != nil {
		return err
	}
	for rows.Next() {
		var x slow
		if err := rows.Scan(&x.TS, &x.DurationMs, &x.Status, &x.SessionKey, &x.UserID, &x.JobID, &x.Release, &x.Attrs); err != nil {
			rows.Close()
			return err
		}
		slowest = append(slowest, x)
	}
	rows.Close()

	type byRelease struct {
		Release string  `json:"release"`
		Count   int64   `json:"count"`
		AvgMs   float64 `json:"avgMs"`
	}
	releases := []byRelease{}
	rows, err = s.db.R.QueryContext(r.Context(), `SELECT release, COUNT(*), AVG(duration_ms) FROM spans
		WHERE project_id = ? AND name = ? AND ts >= ? AND ts < ? GROUP BY release ORDER BY MAX(ts) DESC LIMIT 10`, p.ID, name, tr.From, tr.To)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var x byRelease
		if err := rows.Scan(&x.Release, &x.Count, &x.AvgMs); err != nil {
			return err
		}
		releases = append(releases, x)
	}
	return ok(w, map[string]any{"series": ts, "slowest": slowest, "releases": releases})
}
