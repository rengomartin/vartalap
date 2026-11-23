package handlers

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"

	appmiddleware "vartalap/internal/middleware"
	"vartalap/internal/models"
	"vartalap/internal/services"
)

func (h *handler) page(w http.ResponseWriter, r *http.Request) {
	deviceID := appmiddleware.DeviceID(r.Context())
	user, _, found, err := h.store.GetUserByDeviceID(r.Context(), deviceID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if !found {
		h.renderPage(w, http.StatusOK, "onboarding", models.PageData{
			Title: "Welcome",
			View:  "onboarding",
			CSRF:  appmiddleware.CSRFToken(r.Context()),
		})
		return
	}
	h.renderPage(w, http.StatusOK, "app", models.PageData{
		Title: "Chats",
		View:  "app",
		CSRF:  appmiddleware.CSRFToken(r.Context()),
		User:  &user,
	})
}

func (h *handler) publicProfile(w http.ResponseWriter, r *http.Request) {
	username := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "username")))
	user, found, err := h.store.GetUserByUsername(r.Context(), username)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if !found {
		h.renderPage(w, http.StatusNotFound, "notfound", models.PageData{
			Title: "Profile not found",
			View:  "notfound",
			CSRF:  appmiddleware.CSRFToken(r.Context()),
		})
		return
	}
	summary := user.Summary()
	isSelf := false
	if deviceID := appmiddleware.DeviceID(r.Context()); services.IsValidDeviceID(deviceID) {
		if viewer, _, found, err := h.store.GetUserByDeviceID(r.Context(), deviceID); err == nil && found {
			isSelf = viewer.ID == user.ID
		}
	}
	h.renderPage(w, http.StatusOK, "profile", models.PageData{
		Title:   user.DisplayName + " (@" + user.Username + ")",
		View:    "profile",
		CSRF:    appmiddleware.CSRFToken(r.Context()),
		Profile: &summary,
		IsSelf:  isSelf,
	})
}

func (h *handler) avatar(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	if !services.IsValidUUID(userID) {
		http.NotFound(w, r)
		return
	}
	user, found, err := h.store.GetUserByID(r.Context(), userID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if !found || user.AvatarUpdatedAt == 0 {
		http.NotFound(w, r)
		return
	}
	file, contentType, err := h.files.OpenAvatar(userID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		h.serverError(w, err)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, file)
}

func (h *handler) discoverPartial(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r)
	if !h.allow("discovery", user.ID) {
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}
	page, err := h.directoryPage(r, user.ID, r.URL.Query().Get("cursor"))
	if err != nil {
		h.serverError(w, err)
		return
	}
	fragment, err := h.renderer.Fragment("discover-results", page)
	if err != nil {
		h.serverError(w, err)
		return
	}
	writeHTML(w, http.StatusOK, fragment)
}

func (h *handler) discoverSearchPartial(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r)
	if !h.allow("search", user.ID) {
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	page := models.DirectoryPage{Query: query}
	if query != "" {
		if len(query) > 32 {
			query = query[:32]
		}
		users, err := h.store.SearchUsers(r.Context(), user.ID, query, 20)
		if err != nil {
			h.serverError(w, err)
			return
		}
		page.Users = users
	} else {
		directory, err := h.directoryPage(r, user.ID, "")
		if err != nil {
			h.serverError(w, err)
			return
		}
		page = directory
	}
	fragment, err := h.renderer.Fragment("discover-results", page)
	if err != nil {
		h.serverError(w, err)
		return
	}
	writeHTML(w, http.StatusOK, fragment)
}

func (h *handler) directoryPage(r *http.Request, viewerID, cursor string) (models.DirectoryPage, error) {
	users, err := h.store.ListDiscoverable(r.Context(), viewerID, cursor, 24)
	if err != nil {
		return models.DirectoryPage{}, err
	}
	page := models.DirectoryPage{Users: users}
	if len(users) == 24 {
		page.NextCursor = users[len(users)-1].Username
	}
	return page, nil
}

func (h *handler) renderPage(w http.ResponseWriter, status int, name string, data models.PageData) {
	var buffer bytes.Buffer
	if err := h.renderer.Page(&buffer, name, data); err != nil {
		h.serverError(w, err)
		return
	}
	writeHTML(w, status, buffer.Bytes())
}
