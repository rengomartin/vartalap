package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"vartalap/internal/models"
	"vartalap/internal/services"
)

type sendMessageResponse struct {
	MessageID       string `json:"messageId"`
	ServerCreatedAt int64  `json:"serverCreatedAt"`
	Duplicate       bool   `json:"duplicate"`
}

func (h *handler) sendMessage(w http.ResponseWriter, r *http.Request) {
	if !h.csrfOK(w, r) {
		return
	}
	user := mustUser(r)
	device := mustDevice(r)
	if !h.allow("messages", user.ID) {
		apiError(w, http.StatusTooManyRequests, "rate_limited", "Slow down a little before sending another message.")
		return
	}
	conversationID := chi.URLParam(r, "id")
	conversation, member, err := h.ensureMembership(r, conversationID, user.ID)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	if !member {
		apiError(w, http.StatusForbidden, "forbidden", "You are not part of that conversation.")
		return
	}

	var envelope models.Envelope
	if !decodeJSON(w, r, &envelope, 160*1024) {
		return
	}
	envelope.ConversationID = conversationID
	if err := services.ValidateEnvelope(envelope); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_envelope", err.Error())
		return
	}
	if envelope.SenderUserID != user.ID || envelope.SenderDeviceID != device.ID {
		apiError(w, http.StatusForbidden, "forbidden", "The sender does not match this session.")
		return
	}
	if envelope.RecipientUserID != conversation.Peer.ID {
		apiError(w, http.StatusForbidden, "forbidden", "The recipient is not part of that conversation.")
		return
	}
	if envelope.SenderSigningKey != device.SigPublicKey {
		apiError(w, http.StatusForbidden, "forbidden", "The signing key does not match this device.")
		return
	}
	if err := services.VerifySignature(device.SigPublicKey, envelope.Signature, services.CanonicalMessage(envelope)); err != nil {
		apiError(w, http.StatusForbidden, "invalid_signature", "The message signature could not be verified.")
		return
	}

	recipientDevice, found, err := h.store.GetActiveDeviceForUser(r.Context(), conversation.Peer.ID)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	if !found {
		apiError(w, http.StatusConflict, "no_device", "That user has no active device.")
		return
	}

	now := time.Now().UTC()
	envelope.ServerCreatedAt = now.UnixMilli()
	expiresAt := now.Add(time.Duration(h.cfg.MessageRetentionDays) * 24 * time.Hour)
	inserted, err := h.store.CreateEncryptedMessage(r.Context(), envelope, []string{recipientDevice.ID}, expiresAt)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}

	event := models.MessageEvent{Envelope: envelope, Sender: user.Summary()}
	if inserted {
		h.hub.SendToUserJSON(conversation.Peer.ID, map[string]any{"type": "message", "envelope": event.Envelope, "sender": event.Sender})
		// Echo to the sender's other tabs. Clients deduplicate by message ID.
		h.hub.SendToUserJSON(user.ID, map[string]any{"type": "message", "envelope": event.Envelope, "sender": event.Sender})
		if h.hub.IsOnline(conversation.Peer.ID) {
			_ = h.store.MarkDelivered(r.Context(), envelope.ID, recipientDevice.ID)
		}
	}
	writeJSON(w, http.StatusOK, sendMessageResponse{
		MessageID:       envelope.ID,
		ServerCreatedAt: envelope.ServerCreatedAt,
		Duplicate:       !inserted,
	})
}

func (h *handler) pendingMessages(w http.ResponseWriter, r *http.Request) {
	device := mustDevice(r)
	user := mustUser(r)
	if !h.allow("messages", "pending:"+user.ID) {
		apiError(w, http.StatusTooManyRequests, "rate_limited", "Slow down a little.")
		return
	}
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}
	events, err := h.store.ListPendingMessages(r.Context(), device.ID, limit)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	for _, event := range events {
		_ = h.store.MarkDelivered(r.Context(), event.Envelope.ID, device.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": events})
}

type ackRequest struct {
	MessageIDs []string `json:"messageIds"`
}

func (h *handler) ackMessages(w http.ResponseWriter, r *http.Request) {
	if !h.csrfOK(w, r) {
		return
	}
	user := mustUser(r)
	device := mustDevice(r)
	if !h.allow("ack", user.ID) {
		apiError(w, http.StatusTooManyRequests, "rate_limited", "Slow down a little.")
		return
	}
	var request ackRequest
	if !decodeJSON(w, r, &request, 16*1024) {
		return
	}
	if len(request.MessageIDs) == 0 || len(request.MessageIDs) > 100 {
		apiError(w, http.StatusBadRequest, "invalid_ack", "Invalid acknowledgement batch.")
		return
	}
	for _, id := range request.MessageIDs {
		if !services.IsValidUUID(id) {
			apiError(w, http.StatusBadRequest, "invalid_ack", "Invalid message identifier.")
			return
		}
	}
	results, err := h.store.AckMessages(r.Context(), device.ID, request.MessageIDs)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	for _, result := range results {
		h.hub.SendToUserJSON(result.SenderUserID, map[string]any{
			"type":      "delivery",
			"messageId": result.MessageID,
			"state":     "delivered",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *handler) deleteMessage(w http.ResponseWriter, r *http.Request) {
	if !h.csrfOK(w, r) {
		return
	}
	user := mustUser(r)
	messageID := chi.URLParam(r, "id")
	if !services.IsValidUUID(messageID) {
		apiError(w, http.StatusBadRequest, "invalid_id", "Invalid message identifier.")
		return
	}
	deleted, err := h.store.DeleteUndeliveredMessage(r.Context(), messageID, user.ID)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	if !deleted {
		writeJSON(w, http.StatusConflict, map[string]any{
			"deleted": false,
			"reason":  "already_delivered",
			"message": "This message was already delivered. You can remove it from this device, but not from the recipient's device.",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
