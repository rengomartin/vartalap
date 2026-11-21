package handlers

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"time"

	appmiddleware "vartalap/internal/middleware"
	"vartalap/internal/models"
	"vartalap/internal/services"
)

func (h *handler) page(w http.ResponseWriter, r *http.Request) {
	deviceID := appmiddleware.DeviceID(r.Context())
	user, found, err := h.store.GetUser(r.Context(), deviceID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if !found {
		h.renderWelcome(w, "")
		return
	}
	data, err := h.chatData(r.Context(), deviceID, user.DisplayName)
	if err != nil {
		h.serverError(w, err)
		return
	}
	h.renderChat(w, data)
}

func (h *handler) join(w http.ResponseWriter, r *http.Request) {
	deviceID := appmiddleware.DeviceID(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		h.renderWelcome(w, "That request was too large.")
		return
	}
	if !h.forms.Allow("join:"+deviceID, time.Now()) {
		h.renderWelcome(w, "Too many attempts. Please wait a moment.")
		return
	}
	name, err := services.ValidateDisplayName(r.PostFormValue("display_name"))
	if err != nil {
		h.renderWelcome(w, err.Error())
		return
	}
	user, err := h.store.UpsertUser(r.Context(), deviceID, name)
	if err != nil {
		h.serverError(w, err)
		return
	}
	data, err := h.chatData(r.Context(), deviceID, user.DisplayName)
	if err != nil {
		h.serverError(w, err)
		return
	}
	h.renderChat(w, data)
}

func (h *handler) updateName(w http.ResponseWriter, r *http.Request) {
	deviceID := appmiddleware.DeviceID(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		h.writeNotice(w, "error", "That request was too large.")
		return
	}
	if !h.forms.Allow("name:"+deviceID, time.Now()) {
		h.writeNotice(w, "error", "Too many attempts. Please wait a moment.")
		return
	}
	name, err := services.ValidateDisplayName(r.PostFormValue("display_name"))
	if err != nil {
		h.writeNotice(w, "error", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	user, err := h.store.UpsertUser(ctx, deviceID, name)
	if err != nil {
		h.serverError(w, err)
		return
	}
	nameHTML, err := h.renderer.RenderName(user.DisplayName)
	if err != nil {
		h.serverError(w, err)
		return
	}
	noticeHTML, err := h.renderer.RenderNotice("info", "Display name updated.")
	if err != nil {
		h.serverError(w, err)
		return
	}
	writeHTML(w, http.StatusOK, append(nameHTML, noticeHTML...))
}

// restore re-establishes an identity from a signed token kept in localStorage
// or the sync cookie. This is the fallback path when a browser drops cookies
// between visits: the page still knows who the device is, so the user is not
// asked for a name again.
func (h *handler) restore(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		h.renderWelcome(w, "That saved session could not be read.")
		return
	}
	token := strings.TrimSpace(r.PostFormValue("token"))
	if token == "" {
		h.renderWelcome(w, "No saved session was found on this device.")
		return
	}
	deviceID, ok := h.signer.Verify(token)
	if !ok {
		h.renderWelcome(w, "That saved session is no longer valid. Choose a name to start again.")
		return
	}
	_, found, err := h.store.GetUser(r.Context(), deviceID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if !found {
		h.renderWelcome(w, "No chat profile was found for this device. Choose a name to start.")
		return
	}
	appmiddleware.SetIdentityCookies(w, r, token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// forget removes the remembered identity from this browser. The device ID
// itself is not reused, so a forgotten device starts with a clean identity.
func (h *handler) forget(w http.ResponseWriter, r *http.Request) {
	appmiddleware.ClearIdentityCookies(w, r)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *handler) chatData(ctx context.Context, deviceID, displayName string) (models.PageData, error) {
	fetched, err := h.store.LatestMessages(ctx, h.cfg.HistoryLimit+1)
	if err != nil {
		return models.PageData{}, err
	}
	data := models.PageData{
		Title:       "Public chat",
		DisplayName: displayName,
		DeviceKey:   services.SenderKey(deviceID),
		Online:      h.hub.Online(),
		ViewerID:    deviceID,
		MaxRunes:    h.cfg.MaxMessageRunes,
	}
	if len(fetched) > h.cfg.HistoryLimit {
		data.HasMore = true
		fetched = fetched[1:]
	}
	models.MarkViewer(fetched, deviceID)
	data.Messages = fetched
	if len(fetched) > 0 {
		data.OldestID = fetched[0].ID
	}
	return data, nil
}

func (h *handler) renderChat(w http.ResponseWriter, data models.PageData) {
	var buffer bytes.Buffer
	if err := h.renderer.Page(&buffer, "chat", data); err != nil {
		h.serverError(w, err)
		return
	}
	writeHTML(w, http.StatusOK, buffer.Bytes())
}

func (h *handler) renderWelcome(w http.ResponseWriter, notice string) {
	var buffer bytes.Buffer
	if err := h.renderer.Page(&buffer, "welcome", models.PageData{Title: "Join", Notice: notice}); err != nil {
		h.serverError(w, err)
		return
	}
	writeHTML(w, http.StatusOK, buffer.Bytes())
}

func (h *handler) writeNotice(w http.ResponseWriter, level, text string) {
	fragment, err := h.renderer.RenderNotice(level, text)
	if err != nil {
		h.serverError(w, err)
		return
	}
	writeHTML(w, http.StatusOK, fragment)
}

func (h *handler) serverError(w http.ResponseWriter, err error) {
	if h.logger != nil {
		h.logger.Printf("handler error: %v", err)
	}
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}
