package server

import (
	"context"
	"strconv"
	"time"
)

// runRetention deletes telemetry older than each project's retention settings.
// Issues themselves are kept (with their counters) until their last event ages
// out; player records are kept forever so "new player" stays accurate.
func (s *Server) runRetention(ctx context.Context) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, log_retention_days, data_retention_days FROM projects`)
	if err != nil {
		s.log.Error("retention: list projects", "err", err)
		return
	}
	type proj struct {
		id                int64
		logDays, dataDays int64
	}
	var projects []proj
	for rows.Next() {
		var p proj
		if err := rows.Scan(&p.id, &p.logDays, &p.dataDays); err == nil {
			projects = append(projects, p)
		}
	}
	rows.Close()

	now := time.Now().UnixMilli()
	_, err = s.db.W.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, now)
	if err == nil {
		_, err = s.db.W.ExecContext(ctx, `DELETE FROM notifications WHERE read_at IS NOT NULL AND read_at < ?`, now-30*dayMs)
	}
	if err != nil {
		s.log.Error("retention: sessions", "err", err)
	}

	for _, p := range projects {
		logCutoff := now - p.logDays*dayMs
		dataCutoff := now - p.dataDays*dayMs
		for _, q := range []struct {
			table, where string
			cutoff       int64
		}{
			{"logs", "ts < ?", logCutoff},
			{"heartbeats", "ts < ?", logCutoff},
			{"metrics", "ts < ?", dataCutoff},
			{"error_events", "ts < ?", dataCutoff},
			{"issues", "last_seen < ?", dataCutoff},
			{"events", "ts < ?", dataCutoff},
			{"play_sessions", "started_at < ?", dataCutoff},
			{"game_servers", "last_seen < ? AND closed_at IS NOT NULL", dataCutoff},
			{"spans", "ts < ?", logCutoff},
			{"client_perf", "ts < ?", logCutoff},
			{"detections", "ts < ?", dataCutoff},
			{"alert_history", "created_at < ?", dataCutoff},
			{"audit_log", "created_at < ?", max(dataCutoff, now-365*dayMs)},
		} {
			if err := s.deleteInChunks(ctx, q.table, q.where, p.id, q.cutoff); err != nil {
				s.log.Error("retention", "project", p.id, "table", q.table, "err", err)
			}
		}
	}
	// Reclaim WAL space after big deletes.
	s.db.W.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	s.db.W.ExecContext(ctx, `PRAGMA optimize`)
}

// deleteInChunks deletes matching rows in batches so the single writer
// connection isn't held for long stretches while ingestion is waiting.
func (s *Server) deleteInChunks(ctx context.Context, table, where string, projectID, cutoff int64) error {
	const chunk = 5000
	query := `DELETE FROM ` + table + ` WHERE rowid IN (SELECT rowid FROM ` + table +
		` WHERE project_id = ? AND ` + where + ` LIMIT ` + strconv.Itoa(chunk) + `)`
	for {
		res, err := s.db.W.ExecContext(ctx, query, projectID, cutoff)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n < chunk {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
