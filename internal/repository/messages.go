package repository

import (
	"context"
	"time"

	"vartalap/internal/models"
)

const groupWindow = 5 * time.Minute

// CreateMessage persists a message and fills in its generated ID and timestamp.
func (s *Store) CreateMessage(ctx context.Context, message *models.Message) error {
	message.CreatedAt = time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (device_id, sender_name, content, created_at)
		VALUES (?, ?, ?, ?)`,
		message.DeviceID, message.SenderName, message.Content, message.CreatedAt.UnixMilli())
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	message.ID = id
	return nil
}

// LatestMessages returns the most recent limit messages in chronological order.
func (s *Store) LatestMessages(ctx context.Context, limit int) ([]models.Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, device_id, sender_name, content, created_at
		FROM messages ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	messages, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	reverse(messages)
	markGroups(messages)
	return messages, nil
}

// MessagesBefore returns up to limit messages older than beforeID, chronologically ordered.
func (s *Store) MessagesBefore(ctx context.Context, beforeID int64, limit int) ([]models.Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, device_id, sender_name, content, created_at
		FROM messages WHERE id < ? ORDER BY id DESC LIMIT ?`, beforeID, limit)
	if err != nil {
		return nil, err
	}
	messages, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	reverse(messages)
	markGroups(messages)
	return messages, nil
}

type rowScanner interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

func scanMessages(rows rowScanner) ([]models.Message, error) {
	defer rows.Close()
	messages := make([]models.Message, 0)
	for rows.Next() {
		var message models.Message
		var createdAt int64
		if err := rows.Scan(&message.ID, &message.DeviceID, &message.SenderName, &message.Content, &createdAt); err != nil {
			return nil, err
		}
		message.CreatedAt = time.UnixMilli(createdAt).UTC()
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return messages, nil
}

func reverse(messages []models.Message) {
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
}

func markGroups(messages []models.Message) {
	var previous models.Message
	for index := range messages {
		if index > 0 &&
			messages[index].DeviceID == previous.DeviceID &&
			messages[index].CreatedAt.Sub(previous.CreatedAt) <= groupWindow {
			messages[index].Grouped = true
		}
		previous = messages[index]
	}
}
