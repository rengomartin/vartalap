package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"vartalap/internal/models"
)

// CreateDevice registers a new active device and makes it the primary device.
func (s *Store) CreateDevice(ctx context.Context, device *models.Device) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	if device.CreatedAt.IsZero() {
		device.CreatedAt = now
	}
	if device.LastSeenAt.IsZero() {
		device.LastSeenAt = now
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO devices (id, user_id, name, enc_public_key, sig_public_key, created_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		device.ID, device.UserID, device.Name, device.EncPublicKey, device.SigPublicKey,
		device.CreatedAt.UnixMilli(), device.LastSeenAt.UnixMilli()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET primary_device_id = ?, updated_at = ? WHERE id = ?`,
		device.ID, now.UnixMilli(), device.UserID); err != nil {
		return err
	}
	return tx.Commit()
}

// GetDevice loads a device by ID.
func (s *Store) GetDevice(ctx context.Context, id string) (models.Device, bool, error) {
	var device models.Device
	var createdAt, lastSeen int64
	var revoked sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, name, enc_public_key, sig_public_key, created_at, last_seen_at, revoked_at
		FROM devices WHERE id = ?`, id).
		Scan(&device.ID, &device.UserID, &device.Name, &device.EncPublicKey,
			&device.SigPublicKey, &createdAt, &lastSeen, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Device{}, false, nil
	}
	if err != nil {
		return models.Device{}, false, err
	}
	device.CreatedAt = time.UnixMilli(createdAt).UTC()
	device.LastSeenAt = time.UnixMilli(lastSeen).UTC()
	if revoked.Valid {
		value := time.UnixMilli(revoked.Int64).UTC()
		device.RevokedAt = &value
	}
	return device, true, nil
}

// GetActiveDeviceForUser returns the user's primary active device.
func (s *Store) GetActiveDeviceForUser(ctx context.Context, userID string) (models.Device, bool, error) {
	var device models.Device
	var createdAt, lastSeen int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, name, enc_public_key, sig_public_key, created_at, last_seen_at
		FROM devices
		WHERE user_id = ? AND revoked_at IS NULL
		ORDER BY created_at DESC LIMIT 1`, userID).
		Scan(&device.ID, &device.UserID, &device.Name, &device.EncPublicKey,
			&device.SigPublicKey, &createdAt, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Device{}, false, nil
	}
	if err != nil {
		return models.Device{}, false, err
	}
	device.CreatedAt = time.UnixMilli(createdAt).UTC()
	device.LastSeenAt = time.UnixMilli(lastSeen).UTC()
	return device, true, nil
}

// ListActiveDevices returns every non-revoked device for a user.
func (s *Store) ListActiveDevices(ctx context.Context, userID string) ([]models.Device, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, name, enc_public_key, sig_public_key, created_at, last_seen_at
		FROM devices WHERE user_id = ? AND revoked_at IS NULL ORDER BY created_at ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	devices := make([]models.Device, 0)
	for rows.Next() {
		var device models.Device
		var createdAt, lastSeen int64
		if err := rows.Scan(&device.ID, &device.UserID, &device.Name, &device.EncPublicKey,
			&device.SigPublicKey, &createdAt, &lastSeen); err != nil {
			return nil, err
		}
		device.CreatedAt = time.UnixMilli(createdAt).UTC()
		device.LastSeenAt = time.UnixMilli(lastSeen).UTC()
		devices = append(devices, device)
	}
	return devices, rows.Err()
}

// RevokeDevices revokes every device for a user except keepID.
func (s *Store) RevokeDevices(ctx context.Context, userID, keepID string) error {
	now := time.Now().UTC().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		UPDATE devices SET revoked_at = ?
		WHERE user_id = ? AND id <> ? AND revoked_at IS NULL`, now, userID, keepID)
	return err
}

// TouchDevice updates the last-seen timestamp.
func (s *Store) TouchDevice(ctx context.Context, deviceID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE devices SET last_seen_at = ? WHERE id = ?`, time.Now().UTC().UnixMilli(), deviceID)
	return err
}

// CountActiveDevices returns the number of active devices for a user.
func (s *Store) CountActiveDevices(ctx context.Context, userID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM devices WHERE user_id = ? AND revoked_at IS NULL`, userID).Scan(&count)
	return count, err
}

// RebindDevice atomically makes device the only active device for a recovered
// identity. Any previous binding of the transport device is removed first.
func (s *Store) RebindDevice(ctx context.Context, device *models.Device) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	if device.CreatedAt.IsZero() {
		device.CreatedAt = now
	}
	if device.LastSeenAt.IsZero() {
		device.LastSeenAt = now
	}

	// A recovered browser may previously have held a different account. Drop
	// that stale binding so the primary key can be reused safely.
	if _, err := tx.ExecContext(ctx, `DELETE FROM devices WHERE id = ?`, device.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE devices SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`,
		now.UnixMilli(), device.UserID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO devices (id, user_id, name, enc_public_key, sig_public_key, created_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		device.ID, device.UserID, device.Name, device.EncPublicKey, device.SigPublicKey,
		device.CreatedAt.UnixMilli(), device.LastSeenAt.UnixMilli()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET primary_device_id = ?, updated_at = ? WHERE id = ?`,
		device.ID, now.UnixMilli(), device.UserID); err != nil {
		return err
	}
	return tx.Commit()
}
