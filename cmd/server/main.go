package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"vartalap/internal/config"
	"vartalap/internal/handlers"
	"vartalap/internal/render"
	"vartalap/internal/repository"
	"vartalap/internal/services"
	appws "vartalap/internal/websocket"
)

func main() {
	logger := log.New(os.Stdout, "vartalap: ", log.LstdFlags)
	if err := run(logger); err != nil {
		logger.Fatal(err)
	}
}

func run(logger *log.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	store, err := repository.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.Migrate(context.Background()); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}

	secret, err := store.LoadOrCreateSecret(context.Background(), "identity_signing_key")
	if err != nil {
		return fmt.Errorf("load identity signing key: %w", err)
	}
	signer := services.NewIdentitySigner(secret)

	files, err := services.NewFileStore(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("prepare file store: %w", err)
	}

	renderer, err := render.Parse()
	if err != nil {
		return err
	}

	hub := appws.NewHub(store)
	defer hub.Close()

	handler := handlers.New(handlers.Dependencies{
		Config:   cfg,
		Store:    store,
		Renderer: renderer,
		Hub:      hub,
		Files:    files,
		Limiter:  services.NewLimiter(cfg.RatePerSecond, cfg.RateBurst),
		Signer:   signer,
		Logger:   logger,
	})

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          logger,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go retentionJanitor(ctx, logger, store, files)

	listenErr := make(chan error, 1)
	go func() {
		logger.Printf("listening on %s (db: %s, data: %s, env: %s)",
			cfg.Addr, cfg.DBPath, cfg.DataDir, cfg.Environment)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
		}
	}()

	select {
	case err := <-listenErr:
		return fmt.Errorf("server: %w", err)
	case <-ctx.Done():
		logger.Print("shutting down gracefully")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

// retentionJanitor deletes expired ciphertext, attachment blobs and recovery
// challenges. The server only ever deletes opaque data: it cannot read any of
// it, and local device history is authoritative.
func retentionJanitor(ctx context.Context, logger *log.Logger, store *repository.Store, files *services.FileStore) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	sweep := func() {
		now := time.Now().UTC()
		if removed, err := store.ExpireMessages(ctx, now); err != nil {
			logger.Printf("retention: expire messages: %v", err)
		} else if removed > 0 {
			logger.Printf("retention: removed %d expired message envelopes", removed)
		}
		ids, err := store.DeleteExpiredAttachments(ctx, now)
		if err != nil {
			logger.Printf("retention: expire attachments: %v", err)
		}
		for _, id := range ids {
			if err := files.DeleteAttachment(id); err != nil {
				logger.Printf("retention: delete attachment %s: %v", id, err)
			}
		}
		if err := store.CleanupExpiredChallenges(ctx, now); err != nil {
			logger.Printf("retention: cleanup challenges: %v", err)
		}
	}

	sweep()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}
