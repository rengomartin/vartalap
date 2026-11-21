// Package handlers wires HTTP routes to the application services.
package handlers

import (
	"crypto/rand"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"

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
	Limiter  *services.Limiter
	Signer   *services.IdentitySigner
	Logger   *log.Logger
}

type handler struct {
	cfg      config.Config
	store    *repository.Store
	renderer *render.Renderer
	hub      *appws.Hub
	limiter  *services.Limiter
	forms    *services.Limiter
	signer   *services.IdentitySigner
	logger   *log.Logger
	assets   map[string][]byte
}

// New builds the HTTP router.
func New(deps Dependencies) http.Handler {
	signer := deps.Signer
	if signer == nil {
		// Tests and embedders may omit the signer. Production always supplies
		// one backed by a secret persisted in SQLite.
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			panic("vartalap: generate identity secret: " + err.Error())
		}
		signer = services.NewIdentitySigner(secret)
	}

	h := &handler{
		cfg:      deps.Config,
		store:    deps.Store,
		renderer: deps.Renderer,
		hub:      deps.Hub,
		limiter:  deps.Limiter,
		forms:    services.NewLimiter(1, 5),
		signer:   signer,
		logger:   deps.Logger,
		assets:   loadAssets(),
	}

	router := chi.NewRouter()
	router.Use(chiMiddleware.Recoverer)
	router.Use(appmiddleware.SecurityHeaders)
	router.Use(appmiddleware.RequestLogger(deps.Logger))
	router.Use(appmiddleware.Identity(signer))

	router.Get("/", h.page)
	router.Post("/join", h.join)
	router.Post("/name", h.updateName)
	router.Post("/restore", h.restore)
	router.Post("/forget", h.forget)
	router.Get("/history", h.history)
	router.Get("/ws", h.websocket)
	router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	staticHandler := appmiddleware.Gzip(http.HandlerFunc(h.static))
	router.Get("/static/*", staticHandler.ServeHTTP)

	return router
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
	default:
		return "application/octet-stream"
	}
}

func writeHTML(w http.ResponseWriter, status int, content []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Identity-aware pages must not come from a cache and leak another
	// session's view (or hide a freshly restored identity).
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(content)
}
