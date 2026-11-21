package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"time"
)

// LoadOrCreateSecret returns a persistent server secret, generating and storing
// it on first use. Keeping it in SQLite means signed device tokens survive
// process restarts, so users are not logged out by a deploy.
func (s *Store) LoadOrCreateSecret(ctx context.Context, name string) ([]byte, error) {
	var value []byte
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name = ?`, name).Scan(&value)
	if err == nil && len(value) > 0 {
		return value, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate secret %q: %w", name, err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO app_secrets (name, value, created_at) VALUES (?, ?, ?)`,
		name, secret, time.Now().UTC().UnixMilli()); err != nil {
		return nil, fmt.Errorf("store secret %q: %w", name, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name = ?`, name).Scan(&value); err != nil {
		return nil, fmt.Errorf("read secret %q: %w", name, err)
	}
	return value, nil
}
