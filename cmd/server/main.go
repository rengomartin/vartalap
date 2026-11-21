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

	renderer, err := render.Parse()
	if err != nil {
		return err
	}

	hub := appws.NewHub(renderer)
	go hub.Run()

	handler := handlers.New(handlers.Dependencies{
		Config:   cfg,
		Store:    store,
		Renderer: renderer,
		Hub:      hub,
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

	listenErr := make(chan error, 1)
	go func() {
		logger.Printf("listening on %s (db: %s, env: %s)", cfg.Addr, cfg.DBPath, cfg.Environment)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-listenErr:
		hub.Close()
		return fmt.Errorf("server: %w", err)
	case <-ctx.Done():
		logger.Print("shutting down gracefully")
		hub.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}
