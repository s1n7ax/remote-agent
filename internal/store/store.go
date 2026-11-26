// Package store persists remote-agent's state in SQLite.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (and migrates) the database at path. Use ":memory:" in tests.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_time_format=sqlite"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// SQLite allows one writer; one connection keeps :memory: databases shared
	// and avoids SQLITE_BUSY under concurrent writes.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate db: %w", err)
	}
	if err := addColumns(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate db: %w", err)
	}
	return &Store{db: db}, nil
}

// addedColumns are columns added to a table after it was first created;
// CREATE TABLE IF NOT EXISTS does not add them to an existing database.
var addedColumns = []struct{ table, column, def string }{
	{"projects", "run_timeout_minutes", "INTEGER NOT NULL DEFAULT 0"},
}

// addColumns adds every column of addedColumns that its table lacks.
func addColumns(db *sql.DB) error {
	for _, c := range addedColumns {
		var n int
		err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, c.table, c.column).Scan(&n)
		if err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		if _, err := db.Exec(`ALTER TABLE ` + c.table + ` ADD COLUMN ` + c.column + ` ` + c.def); err != nil {
			return err
		}
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func now() time.Time { return time.Now().UTC() }

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func timePtr(nt sql.NullTime) *time.Time {
	if !nt.Valid {
		return nil
	}
	t := nt.Time.UTC()
	return &t
}

func notFound(err error, what string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", what, ErrNotFound)
	}
	return err
}

// Setting reads a settings value; ok is false when unset.
func (s *Store) Setting(ctx context.Context, key string) (value string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read setting %s: %w", key, err)
	}
	return value, true, nil
}

// SetSetting writes a settings value.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	if err != nil {
		return fmt.Errorf("write setting %s: %w", key, err)
	}
	return nil
}

// AddSpend adds usd to today's spend and returns the new total for today.
func (s *Store) AddSpend(ctx context.Context, day string, usd float64) (float64, error) {
	var total float64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO spend(day, usd) VALUES(?, ?) ON CONFLICT(day) DO UPDATE SET usd = usd + excluded.usd RETURNING usd`,
		day, usd).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("add spend: %w", err)
	}
	return total, nil
}

// Spend returns the spend recorded for day.
func (s *Store) Spend(ctx context.Context, day string) (float64, error) {
	var total float64
	err := s.db.QueryRowContext(ctx, `SELECT usd FROM spend WHERE day = ?`, day).Scan(&total)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read spend: %w", err)
	}
	return total, nil
}
