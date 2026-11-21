package repository

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"vartalap/internal/models"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestLoadOrCreateSecretPersists(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	first, err := store.LoadOrCreateSecret(ctx, "identity")
	if err != nil {
		t.Fatalf("LoadOrCreateSecret: %v", err)
	}
	if len(first) != 32 {
		t.Fatalf("secret length = %d, want 32", len(first))
	}

	second, err := store.LoadOrCreateSecret(ctx, "identity")
	if err != nil {
		t.Fatalf("LoadOrCreateSecret again: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("secret should persist across calls")
	}

	other, err := store.LoadOrCreateSecret(ctx, "another")
	if err != nil {
		t.Fatalf("LoadOrCreateSecret another: %v", err)
	}
	if string(other) == string(first) {
		t.Fatal("different secret names should not share a value")
	}
}

func TestUpsertUser(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	deviceID := "550e8400-e29b-41d4-a716-446655440000"

	_, found, err := store.GetUser(ctx, deviceID)
	if err != nil || found {
		t.Fatalf("new device should have no user, found=%v err=%v", found, err)
	}

	created, err := store.UpsertUser(ctx, deviceID, "Aap")
	if err != nil {
		t.Fatalf("UpsertUser create: %v", err)
	}
	if created.DisplayName != "Aap" || created.DeviceID != deviceID {
		t.Fatalf("unexpected created user: %+v", created)
	}

	updated, err := store.UpsertUser(ctx, deviceID, "Rahul")
	if err != nil {
		t.Fatalf("UpsertUser update: %v", err)
	}
	if updated.DisplayName != "Rahul" {
		t.Fatalf("display name not updated: %+v", updated)
	}
	if !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("created_at should not change: %v vs %v", updated.CreatedAt, created.CreatedAt)
	}
	if updated.UpdatedAt.Before(created.UpdatedAt) {
		t.Fatalf("updated_at should advance")
	}

	stored, found, err := store.GetUser(ctx, deviceID)
	if err != nil || !found || stored.DisplayName != "Rahul" {
		t.Fatalf("GetUser after update: %+v found=%v err=%v", stored, found, err)
	}
}

func TestCreateMessagePersistsFields(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	message := &models.Message{
		DeviceID:   "device-1",
		SenderName: "Aap",
		Content:    "hello <world> & friends",
	}
	if err := store.CreateMessage(ctx, message); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if message.ID <= 0 || message.CreatedAt.IsZero() {
		t.Fatalf("CreateMessage did not populate ID/timestamp: %+v", message)
	}

	messages, err := store.LatestMessages(ctx, 10)
	if err != nil {
		t.Fatalf("LatestMessages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("want 1 message, got %d", len(messages))
	}
	got := messages[0]
	if got.ID != message.ID || got.Content != message.Content || got.SenderName != "Aap" || got.DeviceID != "device-1" {
		t.Fatalf("persisted message mismatch: %+v", got)
	}
}

func TestMessageHistoryPagination(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	for i := 1; i <= 10; i++ {
		if err := store.CreateMessage(ctx, &models.Message{
			DeviceID:   "device-1",
			SenderName: "Aap",
			Content:    fmt.Sprintf("message %d", i),
		}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	latest, err := store.LatestMessages(ctx, 4)
	if err != nil {
		t.Fatalf("LatestMessages: %v", err)
	}
	if len(latest) != 4 || latest[0].Content != "message 7" || latest[3].Content != "message 10" {
		t.Fatalf("unexpected latest page: %+v", latest)
	}

	older, err := store.MessagesBefore(ctx, latest[0].ID, 4)
	if err != nil {
		t.Fatalf("MessagesBefore: %v", err)
	}
	if len(older) != 4 || older[0].Content != "message 3" || older[3].Content != "message 6" {
		t.Fatalf("unexpected older page: %+v", older)
	}

	first, err := store.MessagesBefore(ctx, older[0].ID, 4)
	if err != nil {
		t.Fatalf("MessagesBefore first page: %v", err)
	}
	if len(first) != 2 || first[0].Content != "message 1" || first[1].Content != "message 2" {
		t.Fatalf("unexpected first page: %+v", first)
	}
}

func TestConsecutiveMessagesAreGrouped(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	sequence := []string{"device-1", "device-1", "device-2", "device-1", "device-1"}
	for _, deviceID := range sequence {
		if err := store.CreateMessage(ctx, &models.Message{
			DeviceID: deviceID, SenderName: deviceID, Content: "hi",
		}); err != nil {
			t.Fatalf("CreateMessage: %v", err)
		}
	}

	messages, err := store.LatestMessages(ctx, 10)
	if err != nil {
		t.Fatalf("LatestMessages: %v", err)
	}
	wantGrouped := []bool{false, true, false, false, true}
	for index, message := range messages {
		if message.Grouped != wantGrouped[index] {
			t.Fatalf("message %d grouped=%v, want %v", index, message.Grouped, wantGrouped[index])
		}
	}
}
