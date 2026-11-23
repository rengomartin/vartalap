// Package handlers wires HTTP routes to the application services.
package handlers

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"

	"vartalap/internal/config"
	appmiddleware "vartalap/internal/middleware"
	"vartalap/internal/render"
	"vartalap/internal/repository"
	"vartalap/internal/services"
	appws "vartalap/internal/websocket"
	staticassets "vartalap/static"
)

// Dependencies groups everything the HTTP layer needs.
type Dependencies struct {
	Config   config.Config
	Store    *repository.Store
	Renderer *render.Renderer
	Hub      *appws.Hub
	Files    *services.FileStore
	Limiter  *services.Limiter
	Signer   *services.IdentitySigner
	Logger   *log.Logger
}

type handler struct {
	cfg      config.Config
	store    *repository.Store
	renderer *render.Renderer
	hub      *appws.Hub
	files    *services.FileStore
	signer   *services.IdentitySigner
	logger   *log.Logger
	assets   map[string][]byte
	limiters map[string]*services.Limiter
}

// New builds the HTTP router.
func New(deps Dependencies) http.Handler {
	signer := deps.Signer
	if signer == nil {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			panic("vartalap: generate identity secret: " + err.Error())
		}
		signer = services.NewIdentitySigner(secret)
	}

	cfg := deps.Config
	if cfg.DataDir == "" {
		cfg.DataDir = "data"
	}
	if cfg.MessageRetentionDays <= 0 {
		cfg.MessageRetentionDays = 30
	}
	if cfg.AttachmentRetentionDays <= 0 {
		cfg.AttachmentRetentionDays = 7
	}
	if cfg.MaxAttachmentBytes <= 0 {
		cfg.MaxAttachmentBytes = 10 * 1024 * 1024
	}
	if cfg.MaxAvatarBytes <= 0 {
		cfg.MaxAvatarBytes = 512 * 1024
	}
	if cfg.MaxDevicesPerUser <= 0 {
		cfg.MaxDevicesPerUser = 1
	}

	files := deps.Files
	if files == nil {
		var err error
		files, err = services.NewFileStore(cfg.DataDir)
		if err != nil {
			panic("vartalap: create file store: " + err.Error())
		}
	}

	h := &handler{
		cfg:      cfg,
		store:    deps.Store,
		renderer: deps.Renderer,
		hub:      deps.Hub,
		files:    files,
		signer:   signer,
		logger:   deps.Logger,
		assets:   loadAssets(),
		limiters: map[string]*services.Limiter{
			"general":     deps.Limiter,
			"register":    services.NewLimiter(1.0/600, 5),
			"discovery":   services.NewLimiter(1, 60),
			"search":      services.NewLimiter(0.5, 20),
			"messages":    services.NewLimiter(1, 30),
			"attachments": services.NewLimiter(1.0/180, 10),
			"recovery":    services.NewLimiter(0.2, 5),
			"profile":     services.NewLimiter(0.5, 20),
			"ack":         services.NewLimiter(2, 60),
			"ws-connect":  services.NewLimiter(0.2, 10),
		},
	}
	if h.limiters["general"] == nil {
		h.limiters["general"] = services.NewLimiter(2, 10)
	}

	router := chi.NewRouter()
	router.Use(chiMiddleware.Recoverer)
	router.Use(appmiddleware.SecurityHeaders)
	router.Use(appmiddleware.RequestLogger(deps.Logger))
	router.Use(appmiddleware.Identity(signer))
	router.Use(appmiddleware.CSRF)

	// Pages.
	router.Get("/", h.page)
	router.Get("/u/{username}", h.publicProfile)
	router.Get("/avatar/{userID}", h.avatar)
	router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// HTMX fragments (authenticated).
	router.With(h.requireUser).Get("/discover", h.discoverPartial)
	router.With(h.requireUser).Get("/discover/search", h.discoverSearchPartial)

	// WebSocket.
	router.Get("/ws", h.websocket)

	// Identity endpoints that only need the transport device cookie.
	router.Post("/api/identity/register", h.register)
	router.Post("/api/identity/recovery/start", h.recoveryStart)
	router.Post("/api/identity/recovery/complete", h.recoveryComplete)

	// Authenticated API.
	router.Route("/api", func(api chi.Router) {
		api.Use(h.requireUser)
		api.Get("/me", h.me)
		api.Post("/profile", h.updateProfile)
		api.Post("/profile/avatar", h.uploadAvatar)
		api.Post("/forget", h.forgetDevice)
		api.Get("/directory", h.directory)
		api.Get("/users/search", h.searchUsers)
		api.Get("/users/{username}", h.userByUsername)
		api.Get("/keys/{username}", h.keyBundle)
		api.Post("/conversations", h.createConversation)
		api.Get("/conversations", h.listConversations)
		api.Get("/conversations/{id}", h.getConversation)
		api.Get("/messages/pending", h.pendingMessages)
		api.Post("/conversations/{id}/messages", h.sendMessage)
		api.Post("/messages/ack", h.ackMessages)
		api.Delete("/messages/{id}", h.deleteMessage)
		api.Post("/conversations/{id}/attachments", h.uploadAttachment)
		api.Get("/attachments/{id}", h.downloadAttachment)
	})

	staticHandler := appmiddleware.Gzip(http.HandlerFunc(h.static))
	router.Get("/static/*", staticHandler.ServeHTTP)

	return router
}

func (h *handler) allow(bucket, key string) bool {
	limiter := h.limiters[bucket]
	if limiter == nil {
		limiter = h.limiters["general"]
	}
	return limiter.Allow(bucket+":"+key, time.Now())
}

func (h *handler) static(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	if name == "" {
		http.NotFound(w, r)
		return
	}
	content, ok := h.assets[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType(name))
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any, maxBytes int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_request", "That request could not be read.")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		apiError(w, http.StatusBadRequest, "invalid_request", "That request contained unexpected data.")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func apiError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

func writeHTML(w http.ResponseWriter, status int, content []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(content)
}

func (h *handler) serverError(w http.ResponseWriter, err error) {
	if h.logger != nil {
		h.logger.Printf("handler error: %v", err)
	}
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}

func (h *handler) jsonServerError(w http.ResponseWriter, err error) {
	if h.logger != nil {
		h.logger.Printf("handler error: %v", err)
	}
	apiError(w, http.StatusInternalServerError, "internal_error", "Something went wrong. Please try again.")
}

func (h *handler) csrfOK(w http.ResponseWriter, r *http.Request) bool {
	if appmiddleware.VerifyCSRF(r) {
		return true
	}
	apiError(w, http.StatusForbidden, "csrf_failed", "Your session security token was missing. Reload the page.")
	return false
}

func (h *handler) readBody(w http.ResponseWriter, r *http.Request, maxBytes int64) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	return io.ReadAll(r.Body)
}

var (
	assetsOnce     sync.Once
	embeddedAssets map[string][]byte
)

func loadAssets() map[string][]byte {
	assetsOnce.Do(func() {
		embeddedAssets = make(map[string][]byte)
		_ = fs.WalkDir(staticassets.FS, ".", func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			content, readErr := staticassets.FS.ReadFile(path)
			if readErr != nil {
				return nil
			}
			embeddedAssets[path] = content
			return nil
		})
	})
	return embeddedAssets
}

func contentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		return "application/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".woff2"):
		return "font/woff2"
	case strings.HasSuffix(name, ".woff"):
		return "font/woff"
	case strings.HasSuffix(name, ".json"):
		return "application/json"
	default:
		return "application/octet-stream"
	}
}

func isNotFound(err error) bool {
	return errors.Is(err, repository.ErrNotFound)
}
