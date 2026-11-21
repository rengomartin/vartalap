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
	WriteBufferSize: 4096,
	// Origin is validated explicitly against the request host / allow-list first.
	CheckOrigin: func(*http.Request) bool { return true },
}

func (h *handler) websocket(w http.ResponseWriter, r *http.Request) {
	deviceID := appmiddleware.DeviceID(r.Context())
	if !services.IsValidDeviceID(deviceID) || !allowedOrigin(r, h.cfg.AllowedOrigins) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	_, found, err := h.store.GetUser(r.Context(), deviceID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if !found {
		http.Error(w, "Choose a display name first", http.StatusForbidden)
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
		DeviceID:        deviceID,
		Store:           h.store,
		Limiter:         h.limiter,
		Renderer:        h.renderer,
		Validate:        func(raw string, max int) (string, error) { return services.ValidateContent(raw, max) },
		MaxMessageBytes: h.cfg.MaxMessageBytes,
		MaxMessageRunes: h.cfg.MaxMessageRunes,
		Logger:          h.logger,
	})
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
	// Behind a TLS proxy the request can still look like plain http, so scheme
	// equality is too strict. Same-host origins are safe to accept here; cross-
	// host origins are still rejected above.
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	return strings.EqualFold(parsed.Host, host)
}
