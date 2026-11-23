package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"vartalap/internal/models"
)

// CreateAttachment records an uploaded ciphertext blob.
func (s *Store) CreateAttachment(ctx context.Context, attachment *models.Attachment) error {
	if attachment.CreatedAt.IsZero() {
		attachment.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO encrypted_attachments
			(id, conversation_id, uploader_user_id, ciphertext_size, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		attachment.ID, attachment.ConversationID, attachment.UploaderUserID,
		attachment.CiphertextSize, attachment.CreatedAt.UnixMilli(), attachment.ExpiresAt.UnixMilli())
	return err
}

// GetAttachment loads attachment metadata.
func (s *Store) GetAttachment(ctx context.Context, id string) (models.Attachment, bool, error) {
	var attachment models.Attachment
	var createdAt, expiresAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, conversation_id, uploader_user_id, ciphertext_size, created_at, expires_at
		FROM encrypted_attachments WHERE id = ?`, id).
		Scan(&attachment.ID, &attachment.ConversationID, &attachment.UploaderUserID,
			&attachment.CiphertextSize, &createdAt, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Attachment{}, false, nil
	}
	if err != nil {
		return models.Attachment{}, false, err
	}
	attachment.CreatedAt = time.UnixMilli(createdAt).UTC()
	attachment.ExpiresAt = time.UnixMilli(expiresAt).UTC()
	return attachment, true, nil
}

// DeleteExpiredAttachments removes metadata for expired blobs and returns their
// IDs so the caller can delete the stored ciphertext files.
func (s *Store) DeleteExpiredAttachments(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM encrypted_attachments WHERE expires_at <= ?`, now.UnixMilli())
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := s.db.ExecContext(ctx,
			`DELETE FROM encrypted_attachments WHERE id = ?`, id); err != nil {
			return nil, err
		}
	}
	return ids, nil
}
