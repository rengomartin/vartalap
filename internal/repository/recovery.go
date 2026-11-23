package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CreateRecoveryChallenge stores a single-use recovery nonce.
func (s *Store) CreateRecoveryChallenge(ctx context.Context, userID, nonce string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO recovery_challenges (nonce, user_id, expires_at) VALUES (?, ?, ?)`,
		nonce, userID, expiresAt.UnixMilli())
	return err
}

// ConsumeRecoveryChallenge marks a nonce as used and returns its user ID. It is
// atomic, so a captured challenge cannot be replayed.
func (s *Store) ConsumeRecoveryChallenge(ctx context.Context, nonce string, now time.Time) (string, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback() }()

	var userID string
	err = tx.QueryRowContext(ctx, `
		SELECT user_id FROM recovery_challenges
		WHERE nonce = ? AND used_at IS NULL AND expires_at > ?`, nonce, now.UnixMilli()).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE recovery_challenges SET used_at = ? WHERE nonce = ?`, now.UnixMilli(), nonce); err != nil {
		return "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	return userID, true, nil
}

// CleanupExpiredChallenges deletes expired recovery nonces.
func (s *Store) CleanupExpiredChallenges(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM recovery_challenges WHERE expires_at <= ?`, now.UnixMilli())
	return err
}
