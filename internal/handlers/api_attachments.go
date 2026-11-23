package handlers

import (
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"vartalap/internal/models"
	"vartalap/internal/services"
)

func (h *handler) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	if !h.csrfOK(w, r) {
		return
	}
	user := mustUser(r)
	if !h.allow("attachments", user.ID) {
		apiError(w, http.StatusTooManyRequests, "rate_limited", "Too many attachment uploads. Please wait.")
		return
	}
	conversationID := chi.URLParam(r, "id")
	if _, member, err := h.ensureMembership(r, conversationID, user.ID); err != nil {
		h.jsonServerError(w, err)
		return
	} else if !member {
		apiError(w, http.StatusForbidden, "forbidden", "You are not part of that conversation.")
		return
	}
	if contentType := r.Header.Get("Content-Type"); contentType != "" && contentType != "application/octet-stream" {
		apiError(w, http.StatusUnsupportedMediaType, "invalid_type", "Attachments must be sent as opaque binary data.")
		return
	}
	id, err := services.NewUUID()
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	size, err := h.files.SaveAttachment(id, r.Body, h.cfg.MaxAttachmentBytes)
	if err != nil {
		if err.Error() == "attachment is too large" {
			apiError(w, http.StatusRequestEntityTooLarge, "too_large", "That file is too large.")
			return
		}
		apiError(w, http.StatusBadRequest, "invalid_upload", "That file could not be stored.")
		return
	}
	now := time.Now().UTC()
	attachment := models.Attachment{
		ID:             id,
		ConversationID: conversationID,
		UploaderUserID: user.ID,
		CiphertextSize: size,
		CreatedAt:      now,
		ExpiresAt:      now.Add(time.Duration(h.cfg.AttachmentRetentionDays) * 24 * time.Hour),
	}
	if err := h.store.CreateAttachment(r.Context(), &attachment); err != nil {
		_ = h.files.DeleteAttachment(id)
		h.jsonServerError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":        attachment.ID,
		"size":      attachment.CiphertextSize,
		"expiresAt": attachment.ExpiresAt.UnixMilli(),
	})
}

func (h *handler) downloadAttachment(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	id := chi.URLParam(r, "id")
	if !services.IsValidUUID(id) {
		http.NotFound(w, r)
		return
	}
	if !h.allow("attachments", "download:"+user.ID) {
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}
	attachment, found, err := h.store.GetAttachment(r.Context(), id)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	if _, member, err := h.ensureMembership(r, attachment.ConversationID, user.ID); err != nil {
		h.jsonServerError(w, err)
		return
	} else if !member {
		apiError(w, http.StatusForbidden, "forbidden", "You are not part of that conversation.")
		return
	}
	file, size, err := h.files.OpenAttachment(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="encrypted.bin"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, file)
}
