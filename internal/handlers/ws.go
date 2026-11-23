package handlers

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"

	appmiddleware "vartalap/internal/middleware"
	"vartalap/internal/services"
	appws "vartalap/internal/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 8192,
	// Origin is validated explicitly against the request host / allow-list first.
	CheckOrigin: func(*http.Request) bool { return true },
}

const maxConnectionsPerUser = 5

func (h *handler) websocket(w http.ResponseWriter, r *http.Request) {
	deviceID := appmiddleware.DeviceID(r.Context())
	if !services.IsValidDeviceID(deviceID) || !allowedOrigin(r, h.cfg.AllowedOrigins) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, device, found, err := h.store.GetUserByDeviceID(r.Context(), deviceID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if !found {
		http.Error(w, "Create an identity first", http.StatusForbidden)
		return
	}
	if !h.allow("ws-connect", user.ID) {
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}
	if h.hub.UserConnections(user.ID) >= maxConnectionsPerUser {
		http.Error(w, "Too many open connections", http.StatusTooManyRequests)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		if h.logger != nil {
			h.logger.Printf("websocket upgrade: %v", err)
		}
		return
	}
	client := appws.NewClient(appws.ClientConfig{
		Hub:             h.hub,
		Conn:            conn,
		UserID:          user.ID,
		DeviceID:        device.ID,
		Store:           h.store,
		Limiter:         h.limiters["general"],
		MaxMessageBytes: 64 * 1024,
		Logger:          h.logger,
	})

	onlinePartners := h.hub.OnlinePartners(r.Context(), user.ID)
	client.EnqueueJSON(map[string]any{
		"type":          "ready",
		"userId":        user.ID,
		"onlineUserIds": onlinePartners,
	})

	// Deliver anything that accumulated while the device was offline. The
	// client acknowledges only after decrypting and storing locally.
	events, err := h.store.ListPendingMessages(r.Context(), device.ID, 200)
	if err == nil {
		for _, event := range events {
			client.EnqueueJSON(map[string]any{
				"type":     "message",
				"envelope": event.Envelope,
				"sender":   event.Sender,
			})
			_ = h.store.MarkDelivered(r.Context(), event.Envelope.ID, device.ID)
		}
	}
	_ = h.store.TouchDevice(r.Context(), device.ID)

	client.Start()
}

// allowedOrigin accepts same-origin WebSocket upgrades and an explicit allow-list.
func allowedOrigin(r *http.Request, allowed []string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	for _, candidate := range allowed {
		if strings.EqualFold(origin, candidate) {
			return true
		}
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return false
	}
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	return strings.EqualFold(parsed.Host, host)
}
