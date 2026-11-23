package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"vartalap/internal/models"
	"vartalap/internal/services"
)

// GetOrCreateConversation returns the single conversation for a pair of users,
// creating it if necessary. Concurrent callers converge on the unique index.
func (s *Store) GetOrCreateConversation(ctx context.Context, userA, userB string) (models.Conversation, bool, error) {
	if userA == userB {
		return models.Conversation{}, false, errors.New("cannot create a conversation with yourself")
	}
	if userA > userB {
		userA, userB = userB, userA
	}

	if existing, found, err := s.getConversationByPair(ctx, userA, userB); err != nil {
		return models.Conversation{}, false, err
	} else if found {
		return existing, false, nil
	}

	id, err := services.NewUUID()
	if err != nil {
		return models.Conversation{}, false, err
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return models.Conversation{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO conversations (id, user_a, user_b, created_at) VALUES (?, ?, ?, ?)`,
		id, userA, userB, now.UnixMilli()); err != nil {
		return models.Conversation{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO conversation_members (conversation_id, user_id, joined_at)
		SELECT id, ?, ? FROM conversations WHERE user_a = ? AND user_b = ?`,
		userA, now.UnixMilli(), userA, userB); err != nil {
		return models.Conversation{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO conversation_members (conversation_id, user_id, joined_at)
		SELECT id, ?, ? FROM conversations WHERE user_a = ? AND user_b = ?`,
		userB, now.UnixMilli(), userA, userB); err != nil {
		return models.Conversation{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return models.Conversation{}, false, err
	}

	conversation, found, err := s.getConversationByPair(ctx, userA, userB)
	if err != nil {
		return models.Conversation{}, false, err
	}
	if !found {
		return models.Conversation{}, false, ErrNotFound
	}
	return conversation, true, nil
}

func (s *Store) getConversationByPair(ctx context.Context, userA, userB string) (models.Conversation, bool, error) {
	var conversation models.Conversation
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_a, user_b, created_at FROM conversations
		WHERE user_a = ? AND user_b = ?`, userA, userB).
		Scan(&conversation.ID, &conversation.UserA, &conversation.UserB, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Conversation{}, false, nil
	}
	if err != nil {
		return models.Conversation{}, false, err
	}
	conversation.CreatedAt = time.UnixMilli(createdAt).UTC()
	return conversation, true, nil
}

// GetConversationForUser loads a conversation only when userID is a member.
func (s *Store) GetConversationForUser(ctx context.Context, conversationID, userID string) (models.Conversation, bool, error) {
	var conversation models.Conversation
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT c.id, c.user_a, c.user_b, c.created_at
		FROM conversations c JOIN conversation_members m ON m.conversation_id = c.id
		WHERE c.id = ? AND m.user_id = ?`, conversationID, userID).
		Scan(&conversation.ID, &conversation.UserA, &conversation.UserB, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Conversation{}, false, nil
	}
	if err != nil {
		return models.Conversation{}, false, err
	}
	conversation.CreatedAt = time.UnixMilli(createdAt).UTC()
	peerID := conversation.UserA
	if peerID == userID {
		peerID = conversation.UserB
	}
	peer, found, err := s.GetUserByID(ctx, peerID)
	if err != nil {
		return models.Conversation{}, false, err
	}
	if !found {
		return models.Conversation{}, false, ErrNotFound
	}
	conversation.Peer = peer.Summary()
	return conversation, true, nil
}

// IsConversationMember reports whether userID belongs to a conversation.
func (s *Store) IsConversationMember(ctx context.Context, conversationID, userID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM conversation_members WHERE conversation_id = ? AND user_id = ?`,
		conversationID, userID).Scan(&count)
	return count > 0, err
}

// ListConversationsForUser returns every conversation for a user with the peer
// profile attached.
func (s *Store) ListConversationsForUser(ctx context.Context, userID string) ([]models.Conversation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.user_a, c.user_b, c.created_at
		FROM conversations c JOIN conversation_members m ON m.conversation_id = c.id
		WHERE m.user_id = ?
		ORDER BY c.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	conversations := make([]models.Conversation, 0)
	for rows.Next() {
		var conversation models.Conversation
		var createdAt int64
		if err := rows.Scan(&conversation.ID, &conversation.UserA, &conversation.UserB, &createdAt); err != nil {
			return nil, err
		}
		conversation.CreatedAt = time.UnixMilli(createdAt).UTC()
		peerID := conversation.UserA
		if peerID == userID {
			peerID = conversation.UserB
		}
		peer, found, err := s.GetUserByID(ctx, peerID)
		if err != nil {
			return nil, err
		}
		if found {
			conversation.Peer = peer.Summary()
		}
		conversations = append(conversations, conversation)
	}
	return conversations, rows.Err()
}

// ConversationPartnerIDs returns the other participants of every conversation
// for a user. Used for presence fan-out.
func (s *Store) ConversationPartnerIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT CASE WHEN c.user_a = ? THEN c.user_b ELSE c.user_a END
		FROM conversations c JOIN conversation_members m ON m.conversation_id = c.id
		WHERE m.user_id = ?`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ConversationPeer returns the other participant of a conversation.
func (s *Store) ConversationPeer(ctx context.Context, conversationID, userID string) (string, bool, error) {
	var userA, userB string
	err := s.db.QueryRowContext(ctx, `
		SELECT c.user_a, c.user_b
		FROM conversations c JOIN conversation_members m ON m.conversation_id = c.id
		WHERE c.id = ? AND m.user_id = ?`, conversationID, userID).Scan(&userA, &userB)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if userA == userID {
		return userB, true, nil
	}
	return userA, true, nil
}
