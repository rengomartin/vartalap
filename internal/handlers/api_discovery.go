package handlers

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"vartalap/internal/models"
	"vartalap/internal/services"
)

func (h *handler) directory(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.allow("discovery", user.ID) {
		apiError(w, http.StatusTooManyRequests, "rate_limited", "Too many directory requests. Please wait.")
		return
	}
	page, err := h.directoryPage(r, user.ID, r.URL.Query().Get("cursor"))
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"users":      page.Users,
		"nextCursor": page.NextCursor,
	})
}

func (h *handler) searchUsers(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.allow("search", user.ID) {
		apiError(w, http.StatusTooManyRequests, "rate_limited", "Too many searches. Please wait.")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"users": []models.Summary{}})
		return
	}
	if len(query) > 32 {
		query = query[:32]
	}
	users, err := h.store.SearchUsers(r.Context(), user.ID, query, 20)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (h *handler) userByUsername(w http.ResponseWriter, r *http.Request) {
	viewer := mustUser(r)
	if !h.allow("discovery", viewer.ID) {
		apiError(w, http.StatusTooManyRequests, "rate_limited", "Too many profile requests. Please wait.")
		return
	}
	username := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "username")))
	user, found, err := h.store.GetUserByUsername(r.Context(), username)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	if !found {
		apiError(w, http.StatusNotFound, "not_found", "That user does not exist.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user.Summary()})
}

func (h *handler) keyBundle(w http.ResponseWriter, r *http.Request) {
	viewer := mustUser(r)
	if !h.allow("discovery", viewer.ID) {
		apiError(w, http.StatusTooManyRequests, "rate_limited", "Too many key requests. Please wait.")
		return
	}
	username := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "username")))
	user, found, err := h.store.GetUserByUsername(r.Context(), username)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	if !found {
		apiError(w, http.StatusNotFound, "not_found", "That user does not exist.")
		return
	}
	device, found, err := h.store.GetActiveDeviceForUser(r.Context(), user.ID)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	if !found {
		apiError(w, http.StatusNotFound, "no_device", "That user has no active device.")
		return
	}
	bundle := models.KeyBundle{
		UserID:          user.ID,
		Username:        user.Username,
		DisplayName:     user.DisplayName,
		AvatarUpdatedAt: user.AvatarUpdatedAt,
		DeviceID:        device.ID,
		EncPublicKey:    device.EncPublicKey,
		SigPublicKey:    device.SigPublicKey,
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"bundle":      bundle,
		"fingerprint": services.Fingerprint(device.EncPublicKey, device.SigPublicKey),
	})
}
