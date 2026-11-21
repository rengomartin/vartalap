package repository

import (
	"context"
	"database/sql"
	"time"

	"vartalap/internal/models"
)

// GetUser returns the anonymous user for a device ID.
func (s *Store) GetUser(ctx context.Context, deviceID string) (models.User, bool, error) {
	var user models.User
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT device_id, display_name, created_at, updated_at
		FROM anonymous_users WHERE device_id = ?`, deviceID).
		Scan(&user.DeviceID, &user.DisplayName, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return models.User{}, false, nil
	}
	if err != nil {
		return models.User{}, false, err
	}
	user.CreatedAt = time.UnixMilli(createdAt).UTC()
	user.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return user, true, nil
}

// UpsertUser creates or updates the display name for a device ID.
func (s *Store) UpsertUser(ctx context.Context, deviceID, displayName string) (models.User, error) {
	now := time.Now().UTC().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO anonymous_users (device_id, display_name, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(device_id) DO UPDATE SET
			display_name = excluded.display_name,
			updated_at = excluded.updated_at`,
		deviceID, displayName, now, now)
	if err != nil {
		return models.User{}, err
	}
	user, _, err := s.GetUser(ctx, deviceID)
	return user, err
}
