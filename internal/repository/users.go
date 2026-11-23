package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-sqlite3"

	"vartalap/internal/models"
)

// ErrUsernameTaken is returned when a username is already registered.
var ErrUsernameTaken = errors.New("username already taken")

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

// CreateAccount creates a user, its first device and the encrypted identity
// backup in one transaction.
func (s *Store) CreateAccount(ctx context.Context, user *models.User, device *models.Device, backup models.IdentityBackup) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	if user.UpdatedAt.IsZero() {
		user.UpdatedAt = now
	}
	if device.CreatedAt.IsZero() {
		device.CreatedAt = now
	}
	if device.LastSeenAt.IsZero() {
		device.LastSeenAt = now
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (id, username, display_name, avatar_updated_at, discoverable, primary_device_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, NULL, ?, ?)`,
		user.ID, user.Username, user.DisplayName, user.AvatarUpdatedAt, boolInt(user.Discoverable),
		user.CreatedAt.UnixMilli(), user.UpdatedAt.UnixMilli()); err != nil {
		return mapConstraint(err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO devices (id, user_id, name, enc_public_key, sig_public_key, created_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		device.ID, user.ID, device.Name, device.EncPublicKey, device.SigPublicKey,
		device.CreatedAt.UnixMilli(), device.LastSeenAt.UnixMilli()); err != nil {
		return mapConstraint(err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET primary_device_id = ? WHERE id = ?`, device.ID, user.ID); err != nil {
		return err
	}

	if err := saveBackupTx(ctx, tx, user.ID, backup); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	user.PrimaryDeviceID = device.ID
	return nil
}

// GetUserByID loads a user by primary key.
func (s *Store) GetUserByID(ctx context.Context, id string) (models.User, bool, error) {
	return s.getUser(ctx, `WHERE id = ?`, id)
}

// GetUserByUsername loads a user by canonical username.
func (s *Store) GetUserByUsername(ctx context.Context, username string) (models.User, bool, error) {
	return s.getUser(ctx, `WHERE username = ? COLLATE NOCASE`, username)
}

// GetUserByDeviceID resolves the active user behind a transport device ID.
func (s *Store) GetUserByDeviceID(ctx context.Context, deviceID string) (models.User, models.Device, bool, error) {
	var user models.User
	var device models.Device
	var createdAt, updatedAt, deviceCreated, lastSeen int64
	var discoverable int
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.display_name, u.avatar_updated_at, u.discoverable,
		       COALESCE(u.primary_device_id, ''), u.created_at, u.updated_at,
		       d.id, d.user_id, d.name, d.enc_public_key, d.sig_public_key, d.created_at, d.last_seen_at
		FROM devices d JOIN users u ON u.id = d.user_id
		WHERE d.id = ? AND d.revoked_at IS NULL`, deviceID).
		Scan(&user.ID, &user.Username, &user.DisplayName, &user.AvatarUpdatedAt, &discoverable,
			&user.PrimaryDeviceID, &createdAt, &updatedAt,
			&device.ID, &device.UserID, &device.Name, &device.EncPublicKey, &device.SigPublicKey,
			&deviceCreated, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return models.User{}, models.Device{}, false, nil
	}
	if err != nil {
		return models.User{}, models.Device{}, false, err
	}
	user.Discoverable = discoverable != 0
	user.CreatedAt = time.UnixMilli(createdAt).UTC()
	user.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	device.CreatedAt = time.UnixMilli(deviceCreated).UTC()
	device.LastSeenAt = time.UnixMilli(lastSeen).UTC()
	return user, device, true, nil
}

func (s *Store) getUser(ctx context.Context, where string, arg any) (models.User, bool, error) {
	var user models.User
	var createdAt, updatedAt int64
	var discoverable int
	var primary sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, username, display_name, avatar_updated_at, discoverable,
		       primary_device_id, created_at, updated_at
		FROM users `+where, arg).
		Scan(&user.ID, &user.Username, &user.DisplayName, &user.AvatarUpdatedAt, &discoverable,
			&primary, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.User{}, false, nil
	}
	if err != nil {
		return models.User{}, false, err
	}
	user.Discoverable = discoverable != 0
	user.PrimaryDeviceID = primary.String
	user.CreatedAt = time.UnixMilli(createdAt).UTC()
	user.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return user, true, nil
}

// UpdateProfile changes the display name and directory visibility. When
// username is non-empty it also changes the unique username. Everything happens
// in one transaction so a taken username cannot leave a partial update behind.
func (s *Store) UpdateProfile(ctx context.Context, userID, displayName string, discoverable bool, username string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC().UnixMilli()
	if username != "" {
		result, err := tx.ExecContext(ctx, `
			UPDATE users SET username = ?, updated_at = ? WHERE id = ?`,
			username, now, userID)
		if err != nil {
			return mapConstraint(err)
		}
		if err := requireAffected(result); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE users SET display_name = ?, discoverable = ?, updated_at = ?
		WHERE id = ?`, displayName, boolInt(discoverable), now, userID)
	if err != nil {
		return err
	}
	if err := requireAffected(result); err != nil {
		return err
	}
	return tx.Commit()
}

// SetAvatar records that a new avatar image is available.
func (s *Store) SetAvatar(ctx context.Context, userID string, updatedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE users SET avatar_updated_at = ?, updated_at = ? WHERE id = ?`,
		updatedAt.UnixMilli(), time.Now().UTC().UnixMilli(), userID)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

// ListDiscoverable returns a page of directory-visible users ordered by
// username. afterUsername is the pagination cursor (exclusive).
func (s *Store) ListDiscoverable(ctx context.Context, excludeID, afterUsername string, limit int) ([]models.Summary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, username, display_name, avatar_updated_at, discoverable
		FROM users
		WHERE discoverable = 1 AND id <> ? AND username > ? COLLATE NOCASE
		ORDER BY username COLLATE NOCASE ASC
		LIMIT ?`, excludeID, afterUsername, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSummaries(rows)
}

// SearchUsers performs exact and partial username matching against the public
// directory. Non-discoverable accounts are not searchable.
func (s *Store) SearchUsers(ctx context.Context, excludeID, query string, limit int) ([]models.Summary, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	pattern := "%" + escapeLike(query) + "%"
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, username, display_name, avatar_updated_at, discoverable
		FROM users
		WHERE discoverable = 1 AND id <> ?
		  AND username LIKE ? ESCAPE '\' COLLATE NOCASE
		ORDER BY (username = ? COLLATE NOCASE) DESC, username COLLATE NOCASE ASC
		LIMIT ?`, excludeID, pattern, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSummaries(rows)
}

func scanSummaries(rows *sql.Rows) ([]models.Summary, error) {
	summaries := make([]models.Summary, 0)
	for rows.Next() {
		var summary models.Summary
		var discoverable int
		if err := rows.Scan(&summary.ID, &summary.Username, &summary.DisplayName,
			&summary.AvatarUpdatedAt, &discoverable); err != nil {
			return nil, err
		}
		summary.Discoverable = discoverable != 0
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return value
}

func mapConstraint(err error) error {
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique {
		return ErrUsernameTaken
	}
	return err
}

func requireAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func mustTime(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

func fmtErr(context string, err error) error {
	return fmt.Errorf("%s: %w", context, err)
}
