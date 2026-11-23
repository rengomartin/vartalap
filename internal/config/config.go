// Package config loads and validates runtime configuration from the environment.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration for the server.
type Config struct {
	Addr            string
	DBPath          string
	DataDir         string
	Environment     string
	AllowedOrigins  []string
	HistoryLimit    int
	MaxMessageRunes int
	MaxMessageBytes int64
	RatePerSecond   float64
	RateBurst       int
	ShutdownTimeout time.Duration

	// Messaging limits and retention.
	MaxAttachmentBytes      int64
	MaxAvatarBytes          int64
	MessageRetentionDays    int
	AttachmentRetentionDays int
	MaxDevicesPerUser       int
}

// Load reads configuration from environment variables, applying safe defaults.
func Load() (Config, error) {
	cfg := Config{
		Addr:            env("ADDR", ":8080"),
		DBPath:          env("DB_PATH", "data/vartalap.db"),
		DataDir:         env("DATA_DIR", "data"),
		Environment:     env("APP_ENV", "development"),
		AllowedOrigins:  splitList(os.Getenv("ALLOWED_ORIGINS")),
		HistoryLimit:    intEnv("HISTORY_LIMIT", 50, 1, 200),
		MaxMessageRunes: intEnv("MAX_MESSAGE_RUNES", 2000, 1, 10000),
		MaxMessageBytes: int64(intEnv("MAX_MESSAGE_BYTES", 65536, 1024, 262144)),
		RatePerSecond:   float64(intEnv("RATE_PER_SECOND", 2, 1, 100)),
		RateBurst:       intEnv("RATE_BURST", 5, 1, 100),
		ShutdownTimeout: time.Duration(intEnv("SHUTDOWN_TIMEOUT_SECONDS", 10, 1, 120)) * time.Second,

		MaxAttachmentBytes:      int64(intEnv("MAX_ATTACHMENT_BYTES", 10*1024*1024, 1024, 50*1024*1024)),
		MaxAvatarBytes:          int64(intEnv("MAX_AVATAR_BYTES", 512*1024, 1024, 5*1024*1024)),
		MessageRetentionDays:    intEnv("MESSAGE_RETENTION_DAYS", 30, 1, 365),
		AttachmentRetentionDays: intEnv("ATTACHMENT_RETENTION_DAYS", 7, 1, 90),
		MaxDevicesPerUser:       intEnv("MAX_DEVICES_PER_USER", 1, 1, 10),
	}

	if cfg.DataDir == "" {
		cfg.DataDir = filepath.Dir(cfg.DBPath)
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
