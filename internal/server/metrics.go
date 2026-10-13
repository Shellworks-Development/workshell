package server

import "net/http"

func (s *Server) handleListMetrics(w http.ResponseWriter, r *http.Request, p *project) error {
	tr := parseRange(r, "24h")
	rows, err := s.db.R.QueryContext(r.Context(), `
		SELECT name, kind, SUM(count), SUM(sum), MIN(min), MAX(max), MAX(ts)
		FROM metrics WHERE project_id = ? AND ts >= ? AND ts < ?
		GROUP BY name, kind ORDER BY name`, p.ID, tr.From, tr.To)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct {
		Name     string  `json:"name"`
		Kind     string  `json:"kind"`
		Count    int64   `json:"count"`
		Sum      float64 `json:"sum"`
		Min      float64 `json:"min"`
		Max      float64 `json:"max"`
		LastSeen int64   `json:"lastSeen"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.Name, &x.Kind, &x.Count, &x.Sum, &x.Min, &x.Max, &x.LastSeen); err != nil {
			return err
		}
		out = append(out, x)
	}
	return ok(w, out)
}

// handleMetricSeries charts a custom metric. Counters chart the sum per bucket
// (summed across servers); gauges and timings chart avg/min/max of samples.
func (s *Server) handleMetricSeries(w http.ResponseWriter, r *http.Request, p *project) error {
	tr := parseRange(r, "24h")
	name := r.URL.Query().Get("name")
	if name == "" {
		return badRequest("name is required")
	}
	job := r.URL.Query().Get("jobId")
	ts := newTimeseries(tr, "sum", "avg", "min", "max", "count")
	rows, err := s.db.R.QueryContext(r.Context(), `
		SELECT (ts - ?2) / ?4 * ?4 + ?2, SUM(sum), SUM(sum) / MAX(SUM(count), 1), MIN(min), MAX(max), SUM(count)
		FROM metrics
		WHERE project_id = ?1 AND name = ?5 AND ts >= ?2 AND ts < ?3 AND (?6 = '' OR job_id = ?6)
		GROUP BY 1`, p.ID, tr.From, tr.To, tr.Bucket, name, job)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var b int64
		v := make([]float64, 5)
		if err := rows.Scan(&b, &v[0], &v[1], &v[2], &v[3], &v[4]); err != nil {
			return err
		}
		for i := range v {
			ts.set(i, b, v[i])
		}
	}
	return ok(w, ts)
}
