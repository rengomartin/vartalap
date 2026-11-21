// Package config loads and validates runtime configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration for the server.
type Config struct {
	Addr            string
	DBPath          string
	Environment     string
	AllowedOrigins  []string
	HistoryLimit    int
	MaxMessageRunes int
	MaxMessageBytes int64
	RatePerSecond   float64
	RateBurst       int
	ShutdownTimeout time.Duration
}

// Load reads configuration from environment variables, applying safe defaults.
func Load() (Config, error) {
	cfg := Config{
		Addr:            env("ADDR", ":8080"),
		DBPath:          env("DB_PATH", "data/vartalap.db"),
		Environment:     env("APP_ENV", "development"),
		AllowedOrigins:  splitList(os.Getenv("ALLOWED_ORIGINS")),
		HistoryLimit:    intEnv("HISTORY_LIMIT", 50, 1, 200),
		MaxMessageRunes: intEnv("MAX_MESSAGE_RUNES", 2000, 1, 10000),
		MaxMessageBytes: int64(intEnv("MAX_MESSAGE_BYTES", 16384, 1024, 65536)),
		RatePerSecond:   float64(intEnv("RATE_PER_SECOND", 2, 1, 100)),
		RateBurst:       intEnv("RATE_BURST", 5, 1, 100),
		ShutdownTimeout: time.Duration(intEnv("SHUTDOWN_TIMEOUT_SECONDS", 10, 1, 120)) * time.Second,
	}

	if cfg.Addr == "" {
		return Config{}, fmt.Errorf("ADDR must not be empty")
	}
	if cfg.DBPath == "" {
		return Config{}, fmt.Errorf("DB_PATH must not be empty")
	}
	for _, origin := range cfg.AllowedOrigins {
		if !strings.HasPrefix(origin, "http://") && !strings.HasPrefix(origin, "https://") {
			return Config{}, fmt.Errorf("ALLOWED_ORIGINS entries must start with http:// or https://, got %q", origin)
		}
	}
	return cfg, nil
}

// IsProduction reports whether the server is running in production mode.
func (c Config) IsProduction() bool {
	return strings.EqualFold(c.Environment, "production")
}

func env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func intEnv(key string, fallback, min, max int) int {
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < min || parsed > max {
		return fallback
	}
	return parsed
}

func splitList(raw string) []string {
	var values []string
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(part), "/")); part != "" {
			values = append(values, part)
		}
	}
	return values
}
