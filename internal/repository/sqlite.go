// Package repository implements SQLite-backed persistence.
package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"vartalap/migrations"

	_ "github.com/mattn/go-sqlite3"
)

// Store provides access to the chat database.
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
	// A single connection keeps SQLite write locking simple and predictable.
	// Queries here are short-lived, so this is plenty for a single public room.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return &Store{db: db}, nil
}

// Migrate applies all embedded schema migrations.
func (s *Store) Migrate(ctx context.Context) error {
	statements, err := migrations.SQL()
	if err != nil {
		return err
	}
	for _, statement := range statements {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply migration: %w", err)
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
