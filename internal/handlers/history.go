package handlers

import (
	"net/http"
	"strconv"
	"time"

	appmiddleware "vartalap/internal/middleware"
	"vartalap/internal/models"
)

func (h *handler) history(w http.ResponseWriter, r *http.Request) {
	deviceID := appmiddleware.DeviceID(r.Context())
	before, err := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	if err != nil || before <= 0 {
		http.Error(w, "Invalid history cursor", http.StatusBadRequest)
		return
	}
	if !h.limiter.Allow("history:"+deviceID, time.Now()) {
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}

	fetched, err := h.store.MessagesBefore(r.Context(), before, h.cfg.HistoryLimit+1)
	if err != nil {
		h.serverError(w, err)
		return
	}
	data := models.MessageList{
		ViewerID: deviceID,
		Messages: fetched,
	}
	if len(fetched) > h.cfg.HistoryLimit {
		data.HasMore = true
		data.Messages = fetched[1:]
	}
	models.MarkViewer(data.Messages, deviceID)
	if len(data.Messages) > 0 {
		data.OldestID = data.Messages[0].ID
	}
	fragment, err := h.renderer.Fragment("history", data)
	if err != nil {
		h.serverError(w, err)
		return
	}
	writeHTML(w, http.StatusOK, fragment)
}
