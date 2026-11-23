package handlers

import (
	"context"
	"net/http"
	"strings"

	appmiddleware "vartalap/internal/middleware"
	"vartalap/internal/models"
	"vartalap/internal/services"
)

type userContextKey struct{}
type deviceContextKey struct{}

// requireUser resolves the transport device cookie to an active account. The
// signed cookie is authentication for the session, not a cryptographic
// identity: message authenticity is proved separately by client signatures.
func (h *handler) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deviceID := appmiddleware.DeviceID(r.Context())
		if !services.IsValidDeviceID(deviceID) {
			h.unauthenticated(w, r)
			return
		}
		user, device, found, err := h.store.GetUserByDeviceID(r.Context(), deviceID)
		if err != nil {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				h.jsonServerError(w, err)
			} else {
				h.serverError(w, err)
			}
			return
		}
		if !found {
			h.unauthenticated(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey{}, user)
		ctx = context.WithValue(ctx, deviceContextKey{}, device)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *handler) unauthenticated(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		apiError(w, http.StatusUnauthorized, "unauthenticated", "Create an identity or restore one first.")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func currentUser(r *http.Request) (models.User, bool) {
	user, ok := r.Context().Value(userContextKey{}).(models.User)
	return user, ok
}

func currentDevice(r *http.Request) (models.Device, bool) {
	device, ok := r.Context().Value(deviceContextKey{}).(models.Device)
	return device, ok
}

func mustUser(r *http.Request) models.User {
	user, _ := currentUser(r)
	return user
}

func mustDevice(r *http.Request) models.Device {
	device, _ := currentDevice(r)
	return device
}
