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

// maxSpanScan bounds the overview's single pass over a range's spans.
const maxSpanScan = 2_000_000

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
	// One pass over the range's spans gives counts, errors, averages and
	// percentiles for every operation (instead of an aggregate plus a query per
	// name), and one pass over client samples gives both the per-platform table
	// and the time series.
	type agg struct {
		d      []float64
		errors int64
		sum    float64
	}
	byName := map[string]*agg{}
	rows, err := s.db.R.QueryContext(ctx, `SELECT name, duration_ms, status FROM spans WHERE project_id = ? AND ts >= ? AND ts < ? LIMIT ?`,
		p.ID, tr.From, tr.To, maxSpanScan)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name, status string
		var d float64
		if err := rows.Scan(&name, &d, &status); err != nil {
			rows.Close()
			return err
		}
		a := byName[name]
		if a == nil {
			a = &agg{}
			byName[name] = a
		}
		a.d = append(a.d, d)
		a.sum += d
		if status == "error" {
			a.errors++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	txns := []*txn{}
	minutes := float64(tr.To-tr.From) / 60_000
	for name, a := range byName {
		sort.Float64s(a.d)
		n := float64(len(a.d))
		txns = append(txns, &txn{Name: name, Count: int64(len(a.d)), Errors: a.errors, AvgMs: a.sum / n, PerMinute: n / minutes,
			P50: percentile(a.d, 0.5), P95: percentile(a.d, 0.95), P99: percentile(a.d, 0.99)})
	}
	sort.Slice(txns, func(i, j int) bool {
		if txns[i].Count != txns[j].Count {
			return txns[i].Count > txns[j].Count
		}
		return txns[i].Name < txns[j].Name
	})
	if len(txns) > 100 {
		txns = txns[:100]
	}

	// Client performance by platform, and over time.
	type platformPerf struct {
		Platform string  `json:"platform"`
		Samples  int64   `json:"samples"`
		FPS      float64 `json:"fps"`
		PingMs   float64 `json:"pingMs"`
		MemoryMb float64 `json:"memoryMb"`
		LowFPS   float64 `json:"lowFpsShare"` // share of samples under 30 FPS
	}
	type sums struct{ n, fps, ping, mem, low float64 }
	plat := map[string]*sums{}
	buckets := map[int64]*sums{}
	rows, err = s.db.R.QueryContext(ctx, `SELECT ts, platform, fps, ping_ms, memory_mb FROM client_perf WHERE project_id = ? AND ts >= ? AND ts < ?`,
		p.ID, tr.From, tr.To)
	if err != nil {
		return err
	}
	for rows.Next() {
		var ts int64
		var platform string
		var fps, ping, mem float64
		if err := rows.Scan(&ts, &platform, &fps, &ping, &mem); err != nil {
			rows.Close()
			return err
		}
		if platform == "" {
			platform = "Unknown"
		}
		low := 0.0
		if fps < 30 {
			low = 1
		}
		ps := plat[platform]
		if ps == nil {
			ps = &sums{}
			plat[platform] = ps
		}
		b := (ts-tr.From)/tr.Bucket*tr.Bucket + tr.From
		bs := buckets[b]
		if bs == nil {
			bs = &sums{}
			buckets[b] = bs
		}
		for _, x := range []*sums{ps, bs} {
			x.n++
			x.fps += fps
			x.ping += ping
			x.mem += mem
			x.low += low
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	platforms := []platformPerf{}
	for name, x := range plat {
		platforms = append(platforms, platformPerf{Platform: name, Samples: int64(x.n), FPS: x.fps / x.n, PingMs: x.ping / x.n, MemoryMb: x.mem / x.n, LowFPS: x.low / x.n})
	}
	sort.Slice(platforms, func(i, j int) bool { return platforms[i].Samples > platforms[j].Samples })
	client := newTimeseries(tr, "fps", "pingMs", "memoryMb")
	for b, x := range buckets {
		client.set(0, b, x.fps/x.n)
		client.set(1, b, x.ping/x.n)
		client.set(2, b, x.mem/x.n)
	}

	server, err := s.serverSeries(ctx, p.ID, tr, "")
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"transactions": txns, "platforms": platforms, "client": client, "server": server})
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
		TraceID    string  `json:"traceId"`
	}
	slowest := []slow{}
	rows, err = s.db.R.QueryContext(r.Context(), `SELECT ts, duration_ms, status, session_key, user_id, job_id, release, attrs, trace_id FROM spans
		WHERE project_id = ? AND name = ? AND ts >= ? AND ts < ? ORDER BY duration_ms DESC LIMIT 20`, p.ID, name, tr.From, tr.To)
	if err != nil {
		return err
	}
	for rows.Next() {
		var x slow
		if err := rows.Scan(&x.TS, &x.DurationMs, &x.Status, &x.SessionKey, &x.UserID, &x.JobID, &x.Release, &x.Attrs, &x.TraceID); err != nil {
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

// handleTrace returns every span in one trace, for drawing it as a tree.
// A span's ts is when it finished, so it started at ts - duration.
func (s *Server) handleTrace(w http.ResponseWriter, r *http.Request, p *project) error {
	traceID := r.PathValue("traceId")
	if traceID == "" || len(traceID) > 64 {
		return errNotFound
	}
	type span struct {
		SpanID     string  `json:"spanId"`
		Parent     string  `json:"parent"`
		Name       string  `json:"name"`
		Start      float64 `json:"start"`
		DurationMs float64 `json:"durationMs"`
		Status     string  `json:"status"`
		UserID     *int64  `json:"userId"`
		JobID      string  `json:"jobId"`
		SessionKey string  `json:"sessionKey"`
		Release    string  `json:"release"`
		Attrs      string  `json:"attrs"`
	}
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT span_id, parent, name, ts, duration_ms, status, user_id, job_id, session_key, release, attrs
		FROM spans WHERE project_id = ? AND trace_id = ? ORDER BY ts - duration_ms LIMIT 500`, p.ID, traceID)
	if err != nil {
		return err
	}
	defer rows.Close()
	spans := []span{}
	for rows.Next() {
		var x span
		var ts int64
		if err := rows.Scan(&x.SpanID, &x.Parent, &x.Name, &ts, &x.DurationMs, &x.Status, &x.UserID, &x.JobID, &x.SessionKey, &x.Release, &x.Attrs); err != nil {
			return err
		}
		x.Start = float64(ts) - x.DurationMs
		// Spans from older SDKs have no id of their own; the root's id is the trace id.
		if x.SpanID == "" && x.Parent == "" {
			x.SpanID = traceID
		}
		spans = append(spans, x)
	}
	if len(spans) == 0 {
		return errNotFound
	}
	return ok(w, map[string]any{"traceId": traceID, "spans": spans})
}
