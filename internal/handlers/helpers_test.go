package handlers_test

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vartalap/internal/config"
	"vartalap/internal/handlers"
	"vartalap/internal/models"
	"vartalap/internal/render"
	"vartalap/internal/repository"
	"vartalap/internal/services"
	appws "vartalap/internal/websocket"
)

// testSigner signs the device cookies used by the handler tests. The same
// signer is passed to every app instance so cookies work across requests.
var testSigner = services.NewIdentitySigner([]byte("vartalap-test-identity-secret-32b"))

func testConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		Addr:            ":0",
		DBPath:          filepath.Join(t.TempDir(), "test.db"),
		Environment:     "test",
		HistoryLimit:    50,
		MaxMessageRunes: 2000,
		MaxMessageBytes: 16384,
		RatePerSecond:   100,
		RateBurst:       1000,
		ShutdownTimeout: time.Second,
	}
}

func newApp(t *testing.T, cfg config.Config) (http.Handler, *repository.Store, *appws.Hub) {
	t.Helper()
	store, err := repository.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	renderer, err := render.Parse()
	if err != nil {
		t.Fatalf("Parse templates: %v", err)
	}
	hub := appws.NewHub(renderer)
	go hub.Run()
	t.Cleanup(func() {
		hub.Close()
		_ = store.Close()
	})
	app := handlers.New(handlers.Dependencies{
		Config:   cfg,
		Store:    store,
		Renderer: renderer,
		Hub:      hub,
		Limiter:  services.NewLimiter(cfg.RatePerSecond, cfg.RateBurst),
		Signer:   testSigner,
		Logger:   log.New(io.Discard, "", 0),
	})
	return app, store, hub
}

func newDevice(t *testing.T) string {
	t.Helper()
	id, err := services.NewDeviceID()
	if err != nil {
		t.Fatalf("NewDeviceID: %v", err)
	}
	return id
}

func seedUser(t *testing.T, store *repository.Store, deviceID, name string) {
	t.Helper()
	if _, err := store.UpsertUser(context.Background(), deviceID, name); err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func seedMessage(t *testing.T, store *repository.Store, deviceID, name, content string) models.Message {
	t.Helper()
	message := models.Message{DeviceID: deviceID, SenderName: name, Content: content}
	if err := store.CreateMessage(context.Background(), &message); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	return message
}

func request(app http.Handler, method, target, body, cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != "" {
		req.Header.Set("Cookie", services.DeviceIDCookieName+"="+testSigner.Issue(cookie))
	}
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, req)
	return recorder
}

func cookieByName(recorder *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func cookieValue(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == services.DeviceIDCookieName {
			return cookie.Value
		}
	}
	t.Fatal("device cookie was not set")
	return ""
}

func formBody(values url.Values) string { return values.Encode() }
