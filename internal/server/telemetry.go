package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/workshell/workshell/internal/telemetry"
)

// InitTelemetry runs before HTTP/background work starts. Switching an existing
// instance is an explicit, resumable migration; never silently hide old data.
func (s *Server) InitTelemetry(ctx context.Context) error {
	var backend string
	if err := s.db.R.QueryRowContext(ctx, `SELECT backend FROM telemetry_state WHERE id = 1`).Scan(&backend); err != nil {
		return err
	}
	if s.telemetry == nil {
		if backend != "sqlite" {
			return fmt.Errorf("this data directory uses ClickHouse telemetry; restore WORKSHELL_CLICKHOUSE_* configuration (switching back requires exporting history)")
		}
		return nil
	}
	if backend == "clickhouse" {
		return nil
	}
	var total int64
	for _, table := range telemetry.Tables {
		var n int64
		if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
			return err
		}
		total += n
	}
	if total > 0 && !s.cfg.ClickHouseMigrate {
		return fmt.Errorf("SQLite contains telemetry: back up the data directory and set WORKSHELL_CLICKHOUSE_MIGRATE=true to move it to ClickHouse before serving")
	}
	// Mark before the first external write; a partially completed migration must
	// resume with ClickHouse configured, never reopen as a SQLite-only instance.
	if _, err := s.db.W.ExecContext(ctx, `UPDATE telemetry_state SET backend = 'migrating' WHERE id = 1`); err != nil {
		return err
	}
	for _, table := range telemetry.Tables {
		for {
			rows, err := s.db.R.QueryContext(ctx, `SELECT rowid, * FROM `+table+` ORDER BY rowid LIMIT 5000`)
			if err != nil {
				return err
			}
			cols, err := rows.Columns()
			if err != nil {
				rows.Close()
				return err
			}
			var batch []telemetry.Row
			var last int64
			for rows.Next() {
				values := make([]any, len(cols))
				dest := make([]any, len(cols))
				for i := range values {
					dest[i] = &values[i]
				}
				if err := rows.Scan(dest...); err != nil {
					rows.Close()
					return err
				}
				row := telemetry.Row{}
				last = values[0].(int64)
				row["id"] = last
				for i, name := range cols[1:] {
					row[name] = values[i+1]
				}
				batch = append(batch, row)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			if len(batch) == 0 {
				break
			}
			if err := s.telemetry.Insert(ctx, table, batch); err != nil {
				return fmt.Errorf("migrate %s: %w", table, err)
			}
			if err := s.db.Tx(ctx, func(tx *sql.Tx) error {
				if _, err := tx.ExecContext(ctx, `UPDATE telemetry_state SET next_id = MAX(next_id, ?) WHERE id = 1`, last+1); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE rowid <= ?`, last)
				return err
			}); err != nil {
				return err
			}
			s.log.Info("moved telemetry to ClickHouse", "table", table, "rows", len(batch))
		}
	}
	_, err := s.db.W.ExecContext(ctx, `UPDATE telemetry_state SET backend = 'clickhouse' WHERE id = 1`)
	return err
}

// queryTelemetry is used only for queries over offloaded tables. Mixed
// relational queries must be split at the caller; do not route arbitrary SQL.
func (s *Server) queryTelemetry(ctx context.Context, query string, args ...any) (telemetry.Rows, error) {
	if s.telemetry != nil {
		return s.telemetry.Query(ctx, query, args...)
	}
	return s.db.R.QueryContext(ctx, query, args...)
}

// telemetryInsert collects the existing, normalized INSERT values. The caller
// writes one outbox row in the same SQLite transaction as the application state.
func (w *batchWriter) telemetryInsert(table, columns string, values ...any) (sql.Result, error) {
	row := telemetry.Row{"id": w.telemetryID}
	w.telemetryID++
	cols := strings.Split(columns, ",")
	if len(cols) != len(values) {
		return nil, fmt.Errorf("invalid telemetry insert")
	}
	for i, col := range cols {
		row[strings.TrimSpace(col)] = values[i]
	}
	if w.telemetryRows == nil {
		w.telemetryRows = map[string][]telemetry.Row{}
	}
	w.telemetryRows[table] = append(w.telemetryRows[table], row)
	return telemetryResult(row["id"].(int64)), nil
}

type telemetryResult int64

func (r telemetryResult) LastInsertId() (int64, error) { return int64(r), nil }
func (r telemetryResult) RowsAffected() (int64, error) { return 1, nil }

func (s *Server) telemetryLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := s.flushTelemetry(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("ClickHouse delivery failed; outbox retained for retry", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// flushTelemetry batches outbox entries per table. Entries are removed only
// after every insert succeeds. ReplacingMergeTree + FINAL handle partial retry.
func (s *Server) flushTelemetry(ctx context.Context) error {
	s.telemetryGuard.RLock()
	defer s.telemetryGuard.RUnlock()
	return s.flushTelemetryLocked(ctx)
}
func (s *Server) flushTelemetryLocked(ctx context.Context) error {
	s.telemetryFlush.Lock()
	defer s.telemetryFlush.Unlock()
	if s.telemetry == nil {
		return nil
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, payload FROM telemetry_outbox ORDER BY id LIMIT 64`)
	if err != nil {
		return err
	}
	batch := map[string][]telemetry.Row{}
	var last int64
	size := 0
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&last, &payload); err != nil {
			rows.Close()
			return err
		}
		var entry map[string][]telemetry.Row
		dec := json.NewDecoder(strings.NewReader(string(payload)))
		// Decode ids as numbers without losing precision when copying rows.
		dec.UseNumber()
		if err := dec.Decode(&entry); err != nil {
			rows.Close()
			return err
		}
		for table, data := range entry {
			batch[table] = append(batch[table], data...)
		}
		size += len(payload)
		if size >= 8<<20 {
			break
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if last == 0 {
		return nil
	}
	for _, table := range telemetry.Tables {
		if err := s.telemetry.Insert(ctx, table, batch[table]); err != nil {
			return err
		}
	}
	_, err = s.db.W.ExecContext(ctx, `DELETE FROM telemetry_outbox WHERE id <= ?`, last)
	return err
}

// drainTelemetryLocked is used while erasure/retention excludes new ingest.
func (s *Server) drainTelemetryLocked(ctx context.Context) error {
	for {
		var n int
		if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM telemetry_outbox`).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		if err := s.flushTelemetryLocked(ctx); err != nil {
			return err
		}
	}
}
