package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"vartalap/internal/models"
)

func saveBackupTx(ctx context.Context, tx *sql.Tx, userID string, backup models.IdentityBackup) error {
	now := time.Now().UTC()
	_, err := tx.ExecContext(ctx, `
		INSERT INTO identity_backups (user_id, ciphertext, iv, kdf_salt, kdf_iterations, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			ciphertext = excluded.ciphertext,
			iv = excluded.iv,
			kdf_salt = excluded.kdf_salt,
			kdf_iterations = excluded.kdf_iterations,
			updated_at = excluded.updated_at`,
		userID, backup.Ciphertext, backup.IV, backup.KDFSalt, backup.KDFIterations,
		now.UnixMilli(), now.UnixMilli())
	return err
}

// SaveIdentityBackup upserts the client-encrypted identity backup.
func (s *Store) SaveIdentityBackup(ctx context.Context, userID string, backup models.IdentityBackup) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := saveBackupTx(ctx, tx, userID, backup); err != nil {
		return err
	}
	return tx.Commit()
}

// GetIdentityBackup loads the encrypted backup for a user.
func (s *Store) GetIdentityBackup(ctx context.Context, userID string) (models.IdentityBackup, bool, error) {
	var backup models.IdentityBackup
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id, ciphertext, iv, kdf_salt, kdf_iterations, created_at, updated_at
		FROM identity_backups WHERE user_id = ?`, userID).
		Scan(&backup.UserID, &backup.Ciphertext, &backup.IV, &backup.KDFSalt,
			&backup.KDFIterations, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.IdentityBackup{}, false, nil
	}
	if err != nil {
		return models.IdentityBackup{}, false, err
	}
	backup.CreatedAt = time.UnixMilli(createdAt).UTC()
	backup.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return backup, true, nil
}
