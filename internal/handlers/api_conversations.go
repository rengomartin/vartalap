package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"vartalap/internal/models"
	"vartalap/internal/services"
)

type conversationView struct {
	ID        string         `json:"id"`
	Peer      models.Summary `json:"peer"`
	CreatedAt int64          `json:"createdAt"`
}

func toConversationView(conversation models.Conversation) conversationView {
	return conversationView{
		ID:        conversation.ID,
		Peer:      conversation.Peer,
		CreatedAt: conversation.CreatedAt.UnixMilli(),
	}
}

type createConversationRequest struct {
	Username string `json:"username"`
}

func (h *handler) createConversation(w http.ResponseWriter, r *http.Request) {
	if !h.csrfOK(w, r) {
		return
	}
	user := mustUser(r)
	if !h.allow("general", "conversation:"+user.ID) {
		apiError(w, http.StatusTooManyRequests, "rate_limited", "Slow down a little.")
		return
	}
	var request createConversationRequest
	if !decodeJSON(w, r, &request, 4*1024) {
		return
	}
	username, err := services.ValidateUsername(request.Username)
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid_username", err.Error())
		return
	}
	peer, found, err := h.store.GetUserByUsername(r.Context(), username)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	if !found {
		apiError(w, http.StatusNotFound, "not_found", "That user does not exist.")
		return
	}
	if peer.ID == user.ID {
		apiError(w, http.StatusBadRequest, "self", "You cannot start a conversation with yourself.")
		return
	}
	conversation, _, err := h.store.GetOrCreateConversation(r.Context(), user.ID, peer.ID)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	conversation.Peer = peer.Summary()
	device, found, err := h.store.GetActiveDeviceForUser(r.Context(), peer.ID)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	if !found {
		apiError(w, http.StatusConflict, "no_device", "That user has no active device yet.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"conversation": toConversationView(conversation),
		"bundle": models.KeyBundle{
			UserID:          peer.ID,
			Username:        peer.Username,
			DisplayName:     peer.DisplayName,
			AvatarUpdatedAt: peer.AvatarUpdatedAt,
			DeviceID:        device.ID,
			EncPublicKey:    device.EncPublicKey,
			SigPublicKey:    device.SigPublicKey,
		},
	})
}

func (h *handler) listConversations(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	conversations, err := h.store.ListConversationsForUser(r.Context(), user.ID)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	views := make([]conversationView, 0, len(conversations))
	for _, conversation := range conversations {
		views = append(views, toConversationView(conversation))
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": views})
}

func (h *handler) getConversation(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	conversationID := chi.URLParam(r, "id")
	conversation, found, err := h.store.GetConversationForUser(r.Context(), conversationID, user.ID)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	if !found {
		apiError(w, http.StatusNotFound, "not_found", "That conversation does not exist.")
		return
	}
	device, found, err := h.store.GetActiveDeviceForUser(r.Context(), conversation.Peer.ID)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	if !found {
		apiError(w, http.StatusConflict, "no_device", "That user has no active device.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"conversation": toConversationView(conversation),
		"bundle": models.KeyBundle{
			UserID:          conversation.Peer.ID,
			Username:        conversation.Peer.Username,
			DisplayName:     conversation.Peer.DisplayName,
			AvatarUpdatedAt: conversation.Peer.AvatarUpdatedAt,
			DeviceID:        device.ID,
			EncPublicKey:    device.EncPublicKey,
			SigPublicKey:    device.SigPublicKey,
		},
	})
}

// ensureMembership is used by message and attachment handlers.
func (h *handler) ensureMembership(r *http.Request, conversationID, userID string) (models.Conversation, bool, error) {
	conversation, found, err := h.store.GetConversationForUser(r.Context(), conversationID, userID)
	if err != nil {
		return models.Conversation{}, false, err
	}
	return conversation, found, nil
}
