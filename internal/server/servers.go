package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
)

type gameServer struct {
	JobID        string  `json:"jobId"`
	PlaceID      int64   `json:"placeId"`
	PlaceVersion string  `json:"placeVersion"`
	Release      string  `json:"release"`
	Environment  string  `json:"environment"`
	ServerType   string  `json:"serverType"`
	MaxPlayers   int     `json:"maxPlayers"`
	StartedAt    int64   `json:"startedAt"`
	LastSeen     int64   `json:"lastSeen"`
	ClosedAt     *int64  `json:"closedAt"`
	Players      int     `json:"players"`
	FPS          float64 `json:"fps"`
	HeartbeatMs  float64 `json:"heartbeatMs"`
	MemoryMb     float64 `json:"memoryMb"`
	PingMs       float64 `json:"pingMs"`
	Live         bool    `json:"live"`
}

const serverColumns = `job_id, place_id, place_version, release, environment, server_type, max_players, started_at,
	last_seen, closed_at, players, fps, heartbeat_ms, memory_mb, ping_ms`

func scanServer(row interface{ Scan(...any) error }, cutoff int64) (gameServer, error) {
	var g gameServer
	err := row.Scan(&g.JobID, &g.PlaceID, &g.PlaceVersion, &g.Release, &g.Environment, &g.ServerType, &g.MaxPlayers,
		&g.StartedAt, &g.LastSeen, &g.ClosedAt, &g.Players, &g.FPS, &g.HeartbeatMs, &g.MemoryMb, &g.PingMs)
	g.Live = g.ClosedAt == nil && g.LastSeen > cutoff
	return g, err
}

func (s *Server) handleListServers(w http.ResponseWriter, r *http.Request, p *project) error {
	cutoff := nowMs() - liveWindow.Milliseconds()
	var rows *sql.Rows
	var err error
	if r.URL.Query().Get("state") == "recent" {
		rows, err = s.db.R.QueryContext(r.Context(), `SELECT `+serverColumns+` FROM game_servers
			WHERE project_id = ? AND last_seen > ? ORDER BY last_seen DESC LIMIT 500`,
			p.ID, nowMs()-24*3600_000)
	} else {
		rows, err = s.db.R.QueryContext(r.Context(), `SELECT `+serverColumns+` FROM game_servers
			WHERE project_id = ? AND closed_at IS NULL AND last_seen > ? ORDER BY players DESC, started_at LIMIT 1000`,
			p.ID, cutoff)
	}
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []gameServer{}
	for rows.Next() {
		g, err := scanServer(rows, cutoff)
		if err != nil {
			return err
		}
		out = append(out, g)
	}
	return ok(w, out)
}

// serverSeries returns per-bucket server health metrics, either for a single
// server (jobID set) or averaged across all servers.
func (s *Server) serverSeries(ctx context.Context, projectID int64, tr timeRange, jobID string) (*timeseries, error) {
	ts := newTimeseries(tr, "servers", "fps", "heartbeatMs", "memoryMb", "pingMs", "instances", "sendKbps", "recvKbps")
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT (ts - ?2) / ?4 * ?4 + ?2, COUNT(DISTINCT job_id), AVG(fps), AVG(heartbeat_ms), AVG(memory_mb),
			AVG(ping_ms), AVG(instances), AVG(send_kbps), AVG(recv_kbps)
		FROM heartbeats
		WHERE project_id = ?1 AND ts >= ?2 AND ts < ?3 AND (?5 = '' OR job_id = ?5)
		GROUP BY 1`, projectID, tr.From, tr.To, tr.Bucket, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b int64
		v := make([]float64, 8)
		if err := rows.Scan(&b, &v[0], &v[1], &v[2], &v[3], &v[4], &v[5], &v[6], &v[7]); err != nil {
			return nil, err
		}
		for i := range v {
			ts.set(i, b, v[i])
		}
	}
	return ts, rows.Err()
}

func (s *Server) handleServerStats(w http.ResponseWriter, r *http.Request, p *project) error {
	tr := parseRange(r, "24h")
	health, err := s.serverSeries(r.Context(), p.ID, tr, "")
	if err != nil {
		return err
	}
	players, err := s.ccuSeries(r, p.ID, tr, "")
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"health": health, "players": players})
}

func (s *Server) handleGetServer(w http.ResponseWriter, r *http.Request, p *project) error {
	jobID := r.PathValue("jobId")
	cutoff := nowMs() - liveWindow.Milliseconds()
	g, err := scanServer(s.db.R.QueryRowContext(r.Context(),
		`SELECT `+serverColumns+` FROM game_servers WHERE project_id = ? AND job_id = ?`, p.ID, jobID), cutoff)
	if errors.Is(err, sql.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return err
	}

	tr := parseRange(r, "6h")
	health, err := s.serverSeries(r.Context(), p.ID, tr, jobID)
	if err != nil {
		return err
	}
	players, err := s.ccuSeries(r, p.ID, tr, jobID)
	if err != nil {
		return err
	}

	var errorCount, logCount, sessionCount int64
	err = s.db.R.QueryRowContext(r.Context(), `
		SELECT
			(SELECT COALESCE(SUM(count), 0) FROM error_events WHERE project_id = ?1 AND job_id = ?2),
			(SELECT COUNT(*) FROM logs WHERE project_id = ?1 AND job_id = ?2 AND ts >= ?3),
			(SELECT COUNT(*) FROM play_sessions WHERE project_id = ?1 AND job_id = ?2 AND started_at >= ?3)`,
		p.ID, jobID, g.StartedAt).Scan(&errorCount, &logCount, &sessionCount)
	if err != nil {
		return err
	}

	issues := []map[string]any{}
	rows, err := s.db.R.QueryContext(r.Context(), `
		SELECT i.id, i.title, i.culprit, SUM(e.count), MAX(e.ts) FROM error_events e JOIN issues i ON i.id = e.issue_id
		WHERE e.project_id = ? AND e.job_id = ? GROUP BY i.id ORDER BY 4 DESC LIMIT 10`, p.ID, jobID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, count, last int64
		var title, culprit string
		if err := rows.Scan(&id, &title, &culprit, &count, &last); err != nil {
			return err
		}
		issues = append(issues, map[string]any{"id": id, "title": title, "culprit": culprit, "count": count, "lastSeen": last})
	}

	return ok(w, map[string]any{
		"server":       g,
		"health":       health,
		"players":      players,
		"errorCount":   errorCount,
		"logCount":     logCount,
		"sessionCount": sessionCount,
		"issues":       issues,
	})
}

// closeStaleServers marks servers that stopped heartbeating (crashed or shut
// down without flushing) as closed and ends their open play sessions.
func (s *Server) closeStaleServers(ctx context.Context) {
	cutoff := nowMs() - 5*60_000
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT project_id, job_id, last_seen FROM game_servers WHERE closed_at IS NULL AND last_seen < ?`, cutoff)
		if err != nil {
			return err
		}
		type stale struct {
			project int64
			job     string
			last    int64
		}
		var list []stale
		for rows.Next() {
			var x stale
			if err := rows.Scan(&x.project, &x.job, &x.last); err != nil {
				rows.Close()
				return err
			}
			list = append(list, x)
		}
		rows.Close()
		for _, x := range list {
			w := &batchWriter{ctx: ctx, tx: tx, p: &ingestProject{ID: x.project}, jobID: x.job}
			err := w.endOpenSessions(x.last)
			w.close()
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE game_servers SET closed_at = last_seen, players = 0 WHERE project_id = ? AND job_id = ?`, x.project, x.job); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.log.Error("close stale servers", "err", err)
	}
}
