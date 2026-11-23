// Package repository implements SQLite-backed persistence.
package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vartalap/migrations"

	_ "github.com/mattn/go-sqlite3"
)

// Store provides access to the Vartalap database.
type Store struct {
	db *sql.DB
}

// Open opens (and creates if needed) the SQLite database at path.
func Open(path string) (*Store, error) {
	if strings.HasPrefix(path, "file:") || path == ":memory:" {
		return openDSN(path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	return openDSN(path + "?_busy_timeout=5000&_journal_mode=WAL&_synchronous=NORMAL&_foreign_keys=on")
}

func openDSN(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite serialises writers anyway. A single connection keeps locking
	// predictable and avoids SQLITE_BUSY under the small workload here.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return &Store{db: db}, nil
}

// Migrate applies every embedded migration exactly once. Existing databases
// created before schema_migrations existed are upgraded safely because the
// original migrations are idempotent (CREATE TABLE IF NOT EXISTS).
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	all, err := migrations.All()
	if err != nil {
		return err
	}
	for _, migration := range all {
		var applied int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, migration.Version).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %s: %w", migration.Name, err)
		}
		if applied > 0 {
			continue
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", migration.Name, err)
		}
		if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", migration.Name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			migration.Version, time.Now().UTC().UnixMilli()); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", migration.Name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", migration.Name, err)
		}
	}
	return s.db.PingContext(ctx)
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB exposes the underlying handle for tests that need seed data.
func (s *Store) DB() *sql.DB {
	return s.db
}
