// Package store is the repository layer over SQLite. It is the only package
// that contains SQL (ADR 0008).
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrNotFound means that no row matches.
var ErrNotFound = errors.New("not found")

// TimeLayout is the stored UTC time format.
const TimeLayout = "2006-01-02T15:04:05.000Z"

func formatTime(t time.Time) string { return t.UTC().Format(TimeLayout) }

func parseTime(s string) time.Time {
	t, err := time.Parse(TimeLayout, s)
	if err != nil {
		panic(fmt.Sprintf("store: bad stored time %q", s))
	}
	return t
}

// Store holds a single-connection write pool and a read pool on the same file.
type Store struct {
	w *sql.DB
	r *sql.DB
}

func dsn(path string, readOnly bool) string {
	q := url.Values{}
	for _, p := range []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(5000)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	if readOnly {
		q.Add("_pragma", "query_only(1)")
	}
	return "file:" + path + "?" + q.Encode()
}

// Open opens the database. It does not apply migrations.
func Open(path string) (*Store, error) {
	w, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	if err := w.Ping(); err != nil {
		w.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	r, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)
	return &Store{w: w, r: r}, nil
}

// Close closes both pools.
func (s *Store) Close() error { return errors.Join(s.r.Close(), s.w.Close()) }

// Ping runs SELECT 1 on the read pool.
func (s *Store) Ping(ctx context.Context) error {
	var one int
	return s.r.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

type migration struct {
	version string
	sql     string
}

func migrations() ([]migration, error) {
	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var ms []migration
	for _, n := range names {
		b, err := migrationFS.ReadFile(n)
		if err != nil {
			return nil, err
		}
		ms = append(ms, migration{version: strings.TrimSuffix(strings.TrimPrefix(n, "migrations/"), ".sql"), sql: string(b)})
	}
	return ms, nil
}

const createMigrationsTable = `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`

func (s *Store) appliedVersions(ctx context.Context) (map[string]bool, error) {
	if _, err := s.w.ExecContext(ctx, createMigrationsTable); err != nil {
		return nil, err
	}
	rows, err := s.w.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// Migrate applies all pending migrations in order, each in one transaction.
// It returns the versions it applied.
func (s *Store) Migrate(ctx context.Context) ([]string, error) {
	ms, err := migrations()
	if err != nil {
		return nil, err
	}
	applied, err := s.appliedVersions(ctx)
	if err != nil {
		return nil, err
	}
	var done []string
	for _, m := range ms {
		if applied[m.version] {
			continue
		}
		tx, err := s.w.BeginTx(ctx, nil)
		if err != nil {
			return done, err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			tx.Rollback()
			return done, fmt.Errorf("migration %s: %w", m.version, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)", m.version, formatTime(time.Now())); err != nil {
			tx.Rollback()
			return done, err
		}
		if err := tx.Commit(); err != nil {
			return done, err
		}
		done = append(done, m.version)
	}
	return done, nil
}

// CheckSchema returns an error if the database has pending migrations.
// "serve" calls it at start. Versions applied by a newer release are returned
// as unknown and are not an error: migrations follow the expand/contract rule,
// so a rolled-back binary still works (§2.4).
func (s *Store) CheckSchema(ctx context.Context) (unknown []string, err error) {
	ms, err := migrations()
	if err != nil {
		return nil, err
	}
	applied, err := s.appliedVersions(ctx)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	var pending []string
	for _, m := range ms {
		known[m.version] = true
		if !applied[m.version] {
			pending = append(pending, m.version)
		}
	}
	for v := range applied {
		if !known[v] {
			unknown = append(unknown, v)
		}
	}
	sort.Strings(unknown)
	if len(pending) > 0 {
		return unknown, fmt.Errorf("pending migrations %v; run \"health-api migrate\"", pending)
	}
	return unknown, nil
}

// inTx runs fn in a write transaction.
func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
