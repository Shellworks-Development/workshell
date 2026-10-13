// Package db opens Workshell's SQLite database and applies schema migrations.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"

	_ "modernc.org/sqlite"
)

// DB wraps two connection pools over the same SQLite file: a single-connection
// pool for writes (SQLite allows one writer at a time) and a wider pool for
// reads, which WAL mode lets run concurrently with the writer.
type DB struct {
	W *sql.DB
	R *sql.DB
}

func Open(dataDir string) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	path := filepath.Join(dataDir, "workshell.db")

	pragmas := url.Values{}
	for _, p := range []string{
		"journal_mode(WAL)",
		"busy_timeout(10000)",
		"foreign_keys(ON)",
		"synchronous(NORMAL)",
		"temp_store(MEMORY)",
		"cache_size(-32000)",
	} {
		pragmas.Add("_pragma", p)
	}
	dsn := "file:" + path + "?" + pragmas.Encode()

	w, err := sql.Open("sqlite", dsn+"&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetConnMaxIdleTime(0)

	r, err := sql.Open("sqlite", dsn+"&mode=ro")
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(max(4, runtime.NumCPU()))

	d := &DB{W: w, R: r}
	if err := d.migrate(context.Background()); err != nil {
		d.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

func (d *DB) Close() error {
	d.R.Close()
	return d.W.Close()
}

// Tx runs fn inside a write transaction, committing if fn returns nil.
func (d *DB) Tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (d *DB) migrate(ctx context.Context) error {
	var version int
	if err := d.W.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	for i := version; i < len(migrations); i++ {
		err := d.Tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			_, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1))
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}
