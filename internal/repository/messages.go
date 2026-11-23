package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"vartalap/internal/models"
)

// AckResult describes a message that became fully acknowledged.
type AckResult struct {
	MessageID      string
	ConversationID string
	SenderUserID   string
	SenderDeviceID string
}

// CreateEncryptedMessage stores an envelope and queues it for recipient
// devices. It is idempotent on the client-generated message ID.
func (s *Store) CreateEncryptedMessage(ctx context.Context, envelope models.Envelope, recipientDeviceIDs []string, expiresAt time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO encrypted_messages
			(id, conversation_id, sender_user_id, sender_device_id, recipient_user_id,
			 envelope_type, ephemeral_public_key, iv, ciphertext, signature, sender_signing_key,
			 client_created_at, server_created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		envelope.ID, envelope.ConversationID, envelope.SenderUserID, envelope.SenderDeviceID,
		envelope.RecipientUserID, envelope.Type, envelope.EphemeralPublicKey, envelope.IV,
		envelope.Ciphertext, envelope.Signature, envelope.SenderSigningKey,
		envelope.ClientCreatedAt, envelope.ServerCreatedAt, expiresAt.UnixMilli())
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		// Duplicate delivery: the caller can treat this as success.
		return false, tx.Commit()
	}
	for _, deviceID := range recipientDeviceIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO message_delivery (message_id, recipient_device_id, state)
			VALUES (?, ?, 'queued')`, envelope.ID, deviceID); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

// ListPendingMessages returns undelivered envelopes for a device.
func (s *Store) ListPendingMessages(ctx context.Context, deviceID string, limit int) ([]models.MessageEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.conversation_id, m.sender_user_id, m.sender_device_id, m.recipient_user_id,
		       m.envelope_type, m.ephemeral_public_key, m.iv, m.ciphertext, m.signature,
		       m.sender_signing_key, m.client_created_at, m.server_created_at,
		       u.id, u.username, u.display_name, u.avatar_updated_at, u.discoverable
		FROM encrypted_messages m
		JOIN message_delivery d ON d.message_id = m.id
		JOIN users u ON u.id = m.sender_user_id
		WHERE d.recipient_device_id = ? AND d.state <> 'acked' AND m.expires_at > ?
		ORDER BY m.server_created_at ASC, m.id ASC
		LIMIT ?`, deviceID, time.Now().UTC().UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]models.MessageEvent, 0)
	for rows.Next() {
		var event models.MessageEvent
		var clientAt, serverAt int64
		var discoverable int
		if err := rows.Scan(
			&event.Envelope.ID, &event.Envelope.ConversationID, &event.Envelope.SenderUserID,
			&event.Envelope.SenderDeviceID, &event.Envelope.RecipientUserID, &event.Envelope.Type,
			&event.Envelope.EphemeralPublicKey, &event.Envelope.IV, &event.Envelope.Ciphertext,
			&event.Envelope.Signature, &event.Envelope.SenderSigningKey,
			&clientAt, &serverAt,
			&event.Sender.ID, &event.Sender.Username, &event.Sender.DisplayName,
			&event.Sender.AvatarUpdatedAt, &discoverable); err != nil {
			return nil, err
		}
		event.Envelope.ClientCreatedAt = clientAt
		event.Envelope.ServerCreatedAt = serverAt
		event.Sender.Discoverable = discoverable != 0
		events = append(events, event)
	}
	return events, rows.Err()
}

// MarkDelivered records that an envelope reached the recipient device.
func (s *Store) MarkDelivered(ctx context.Context, messageID, deviceID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE message_delivery SET state = 'delivered', delivered_at = ?
		WHERE message_id = ? AND recipient_device_id = ? AND state = 'queued'`,
		time.Now().UTC().UnixMilli(), messageID, deviceID)
	return err
}

// AckMessages acknowledges envelopes and deletes any message whose recipient
// devices have all acknowledged it. Deleted rows are no longer needed: local
// history lives on the devices.
func (s *Store) AckMessages(ctx context.Context, deviceID string, messageIDs []string) ([]AckResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC().UnixMilli()
	results := make([]AckResult, 0)
	for _, messageID := range messageIDs {
		if _, err := tx.ExecContext(ctx, `
			UPDATE message_delivery SET state = 'acked', acked_at = ?
			WHERE message_id = ? AND recipient_device_id = ?`,
			now, messageID, deviceID); err != nil {
			return nil, err
		}
		var result AckResult
		err := tx.QueryRowContext(ctx, `
			SELECT m.id, m.conversation_id, m.sender_user_id, m.sender_device_id
			FROM encrypted_messages m
			WHERE m.id = ?
			  AND NOT EXISTS (
				SELECT 1 FROM message_delivery d
				WHERE d.message_id = m.id AND d.state <> 'acked'
			  )`, messageID).
			Scan(&result.MessageID, &result.ConversationID, &result.SenderUserID, &result.SenderDeviceID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM encrypted_messages WHERE id = ?`, messageID); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return results, nil
}

// DeleteUndeliveredMessage lets a sender remove an envelope that no recipient
// device has received yet. Returns false once any delivery has left the queue.
func (s *Store) DeleteUndeliveredMessage(ctx context.Context, messageID, senderUserID string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM encrypted_messages
		WHERE id = ? AND sender_user_id = ?
		  AND NOT EXISTS (
			SELECT 1 FROM message_delivery d
			WHERE d.message_id = encrypted_messages.id AND d.state <> 'queued'
		  )`, messageID, senderUserID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// ExpireMessages deletes undelivered envelopes past their retention deadline.
func (s *Store) ExpireMessages(ctx context.Context, now time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM encrypted_messages WHERE expires_at <= ?`, now.UnixMilli())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// GetMessageSender returns the sender of an envelope (used for diagnostics).
func (s *Store) GetMessageSender(ctx context.Context, messageID string) (string, bool, error) {
	var sender string
	err := s.db.QueryRowContext(ctx,
		`SELECT sender_user_id FROM encrypted_messages WHERE id = ?`, messageID).Scan(&sender)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return sender, true, nil
}
