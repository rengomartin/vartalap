package repository

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"vartalap/internal/models"
	"vartalap/internal/services"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedAccount(t *testing.T, store *Store, username string) (models.User, models.Device) {
	t.Helper()
	userID, _ := services.NewUUID()
	deviceID, _ := services.NewDeviceID()
	now := time.Now().UTC()
	user := models.User{
		ID: userID, Username: username, DisplayName: "Test " + username,
		Discoverable: true, CreatedAt: now, UpdatedAt: now,
	}
	device := models.Device{
		ID: deviceID, UserID: userID, Name: "Test device",
		EncPublicKey: "enc-" + deviceID, SigPublicKey: "sig-" + deviceID,
		CreatedAt: now, LastSeenAt: now,
	}
	backup := models.IdentityBackup{
		UserID: userID, Ciphertext: "ciphertext", IV: "iv", KDFSalt: "salt",
		KDFIterations: 210000,
	}
	if err := store.CreateAccount(context.Background(), &user, &device, backup); err != nil {
		t.Fatalf("create account: %v", err)
	}
	return user, device
}

func TestCreateAccountAndUsernameUniqueness(t *testing.T) {
	store := testStore(t)
	user, device := seedAccount(t, store, "neel")
	found, _, ok, err := store.GetUserByDeviceID(context.Background(), device.ID)
	if err != nil || !ok || found.ID != user.ID {
		t.Fatalf("GetUserByDeviceID = %v, %v, %v", found.ID, ok, err)
	}

	duplicate := models.User{
		ID: mustUUID(t), Username: "neel", DisplayName: "Copy", Discoverable: true,
	}
	duplicateDevice := models.Device{
		ID: mustDeviceID(t), UserID: duplicate.ID, Name: "x",
		EncPublicKey: "e", SigPublicKey: "s",
	}
	err = store.CreateAccount(context.Background(), &duplicate, &duplicateDevice, models.IdentityBackup{})
	if !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("duplicate username error = %v, want ErrUsernameTaken", err)
	}
}

func TestConversationIsUniquePerPair(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	first, _ := seedAccount(t, store, "alice")
	second, _ := seedAccount(t, store, "bob")

	conversation, created, err := store.GetOrCreateConversation(ctx, first.ID, second.ID)
	if err != nil || !created {
		t.Fatalf("create conversation = %v, created=%v", err, created)
	}
	reversed, createdAgain, err := store.GetOrCreateConversation(ctx, second.ID, first.ID)
	if err != nil || createdAgain {
		t.Fatalf("reversed conversation = %v, created=%v", err, createdAgain)
	}
	if conversation.ID != reversed.ID {
		t.Fatalf("pair should share one conversation: %s vs %s", conversation.ID, reversed.ID)
	}
	if _, _, err := store.GetOrCreateConversation(ctx, first.ID, first.ID); err == nil {
		t.Fatal("self conversation should fail")
	}
	if _, member, err := store.GetConversationForUser(ctx, conversation.ID, second.ID); err != nil || !member {
		t.Fatalf("second user should be a member: %v", err)
	}
}

func TestMessageDeliveryLifecycle(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	sender, senderDevice := seedAccount(t, store, "sender")
	recipient, recipientDevice := seedAccount(t, store, "recipient")
	conversation, _, _ := store.GetOrCreateConversation(ctx, sender.ID, recipient.ID)

	envelope := models.Envelope{
		ID: mustUUID(t), ConversationID: conversation.ID,
		SenderUserID: sender.ID, SenderDeviceID: senderDevice.ID,
		RecipientUserID: recipient.ID, Type: "text",
		EphemeralPublicKey: "ephemeral", IV: "iv", Ciphertext: "ciphertext",
		Signature: "signature", SenderSigningKey: senderDevice.SigPublicKey,
		ClientCreatedAt: time.Now().UTC().UnixMilli(), ServerCreatedAt: time.Now().UTC().UnixMilli(),
	}
	expires := time.Now().Add(time.Hour)
	inserted, err := store.CreateEncryptedMessage(ctx, envelope, []string{recipientDevice.ID}, expires)
	if err != nil || !inserted {
		t.Fatalf("create message = %v, inserted=%v", err, inserted)
	}
	duplicate, err := store.CreateEncryptedMessage(ctx, envelope, []string{recipientDevice.ID}, expires)
	if err != nil || duplicate {
		t.Fatalf("duplicate message = %v, inserted=%v", err, duplicate)
	}

	pending, err := store.ListPendingMessages(ctx, recipientDevice.ID, 10)
	if err != nil || len(pending) != 1 || pending[0].Envelope.ID != envelope.ID {
		t.Fatalf("pending = %#v, %v", pending, err)
	}
	if pending[0].Sender.Username != "sender" {
		t.Fatalf("sender summary = %#v", pending[0].Sender)
	}
	if err := store.MarkDelivered(ctx, envelope.ID, recipientDevice.ID); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	results, err := store.AckMessages(ctx, recipientDevice.ID, []string{envelope.ID})
	if err != nil || len(results) != 1 || results[0].SenderUserID != sender.ID {
		t.Fatalf("ack = %#v, %v", results, err)
	}
	pending, _ = store.ListPendingMessages(ctx, recipientDevice.ID, 10)
	if len(pending) != 0 {
		t.Fatalf("acked message should be deleted, got %d", len(pending))
	}
	if _, found, _ := store.GetMessageSender(ctx, envelope.ID); found {
		t.Fatal("acked message row should be gone")
	}
}

func TestDeleteUndeliveredMessage(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	sender, senderDevice := seedAccount(t, store, "sender")
	recipient, recipientDevice := seedAccount(t, store, "recipient")
	conversation, _, _ := store.GetOrCreateConversation(ctx, sender.ID, recipient.ID)

	envelope := models.Envelope{
		ID: mustUUID(t), ConversationID: conversation.ID,
		SenderUserID: sender.ID, SenderDeviceID: senderDevice.ID,
		RecipientUserID: recipient.ID, Type: "text",
		EphemeralPublicKey: "e", IV: "i", Ciphertext: "c", Signature: "s",
		SenderSigningKey: senderDevice.SigPublicKey, ClientCreatedAt: time.Now().UnixMilli(),
		ServerCreatedAt: time.Now().UnixMilli(),
	}
	if _, err := store.CreateEncryptedMessage(ctx, envelope, []string{recipientDevice.ID}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.DeleteUndeliveredMessage(ctx, envelope.ID, sender.ID)
	if err != nil || !deleted {
		t.Fatalf("delete = %v, %v", deleted, err)
	}
	deleted, _ = store.DeleteUndeliveredMessage(ctx, envelope.ID, sender.ID)
	if deleted {
		t.Fatal("second delete should report nothing removed")
	}
}

func TestExpiredMessagesAndAttachments(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	sender, senderDevice := seedAccount(t, store, "sender")
	recipient, _ := seedAccount(t, store, "recipient")
	conversation, _, _ := store.GetOrCreateConversation(ctx, sender.ID, recipient.ID)

	envelope := models.Envelope{
		ID: mustUUID(t), ConversationID: conversation.ID,
		SenderUserID: sender.ID, SenderDeviceID: senderDevice.ID,
		RecipientUserID: recipient.ID, Type: "text",
		EphemeralPublicKey: "e", IV: "i", Ciphertext: "c", Signature: "s",
		SenderSigningKey: senderDevice.SigPublicKey, ClientCreatedAt: time.Now().UnixMilli(),
		ServerCreatedAt: time.Now().UnixMilli(),
	}
	if _, err := store.CreateEncryptedMessage(ctx, envelope, nil, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if removed, err := store.ExpireMessages(ctx, time.Now()); err != nil || removed != 1 {
		t.Fatalf("expire messages = %d, %v", removed, err)
	}

	expiredID := mustUUID(t)
	if err := store.CreateAttachment(ctx, &models.Attachment{
		ID: expiredID, ConversationID: conversation.ID, UploaderUserID: sender.ID,
		CiphertextSize: 10, CreatedAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	liveID := mustUUID(t)
	if err := store.CreateAttachment(ctx, &models.Attachment{
		ID: liveID, ConversationID: conversation.ID, UploaderUserID: sender.ID,
		CiphertextSize: 10, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	ids, err := store.DeleteExpiredAttachments(ctx, time.Now())
	if err != nil || len(ids) != 1 || ids[0] != expiredID {
		t.Fatalf("expired attachments = %#v, %v", ids, err)
	}
	if _, found, _ := store.GetAttachment(ctx, liveID); !found {
		t.Fatal("live attachment should remain")
	}
}

func TestRecoveryChallengeIsSingleUse(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	user, _ := seedAccount(t, store, "recover")
	nonce := "challenge-nonce"
	if err := store.CreateRecoveryChallenge(ctx, user.ID, nonce, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	userID, ok, err := store.ConsumeRecoveryChallenge(ctx, nonce, time.Now())
	if err != nil || !ok || userID != user.ID {
		t.Fatalf("consume = %q, %v, %v", userID, ok, err)
	}
	if _, ok, _ := store.ConsumeRecoveryChallenge(ctx, nonce, time.Now()); ok {
		t.Fatal("challenge must be single-use")
	}

	expired := "expired-nonce"
	if err := store.CreateRecoveryChallenge(ctx, user.ID, expired, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.ConsumeRecoveryChallenge(ctx, expired, time.Now()); ok {
		t.Fatal("expired challenge must be rejected")
	}
}

func TestRebindDeviceRevokesPrevious(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	user, oldDevice := seedAccount(t, store, "rebind")
	other, otherDevice := seedAccount(t, store, "other")

	replacement := models.Device{
		ID: otherDevice.ID, UserID: user.ID, Name: "Recovered",
		EncPublicKey: oldDevice.EncPublicKey, SigPublicKey: oldDevice.SigPublicKey,
	}
	if err := store.RebindDevice(ctx, &replacement); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	found, _, ok, err := store.GetUserByDeviceID(ctx, replacement.ID)
	if err != nil || !ok || found.ID != user.ID {
		t.Fatalf("recovered device resolves to %q, %v, %v", found.ID, ok, err)
	}
	if _, _, ok, _ := store.GetUserByDeviceID(ctx, oldDevice.ID); ok {
		t.Fatal("old device should be revoked")
	}
	if _, found, _ := store.GetUserByID(ctx, other.ID); !found {
		t.Fatal("the other account should still exist")
	}
	active, err := store.CountActiveDevices(ctx, user.ID)
	if err != nil || active != 1 {
		t.Fatalf("active devices = %d, %v", active, err)
	}
}

func mustUUID(t *testing.T) string {
	t.Helper()
	id, err := services.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustDeviceID(t *testing.T) string {
	t.Helper()
	id, err := services.NewDeviceID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
