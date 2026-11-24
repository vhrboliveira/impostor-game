// Package config reads the application settings from environment variables (12-factor).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// Environments the application can run in.
const (
	EnvDev  = "dev"
	EnvProd = "prod"
)

const (
	defaultPort         = "8080"
	defaultLogLevel     = "info"
	defaultRoomTTL      = 24 * time.Hour
	defaultEmptyRoomTTL = time.Hour
	defaultDevOrigin    = "http://localhost:5173"
)

// Config holds the validated application settings.
type Config struct {
	Env         string
	Port        string
	DatabaseURL string
	LogLevel    slog.Level
	// AllowedOrigins are the origins allowed to open WebSocket connections.
	AllowedOrigins []string
	// RoomTTL is the maximum lifetime of a room, even if it still has players.
	RoomTTL time.Duration
	// EmptyRoomTTL is how long an empty room is kept before being deleted.
	EmptyRoomTTL time.Duration
}

// IsProd reports whether the application runs in production.
func (c Config) IsProd() bool {
	return c.Env == EnvProd
}

// DatabaseURL reads and validates only DATABASE_URL. It is used by the CLIs (migrate, seed),
// which do not need the rest of the settings.
func DatabaseURL(getenv func(string) string) (string, error) {
	url := getenv("DATABASE_URL")
	if url == "" {
		return "", errors.New("DATABASE_URL is required")
	}
	return url, nil
}

// Load reads the configuration using getenv (usually os.Getenv) and reports every problem
// found at once.
func Load(getenv func(string) string) (Config, error) {
	var errs []error

	cfg := Config{
		Env:  valueOr(getenv, "APP_ENV", EnvDev),
		Port: valueOr(getenv, "PORT", defaultPort),
	}

	if cfg.Env != EnvDev && cfg.Env != EnvProd {
		errs = append(errs, fmt.Errorf("APP_ENV must be %q or %q, got %q", EnvDev, EnvProd, cfg.Env))
	}

	if port, err := strconv.Atoi(cfg.Port); err != nil || port < 1 || port > 65535 {
		errs = append(errs, fmt.Errorf("PORT must be a number between 1 and 65535, got %q", cfg.Port))
	}

	databaseURL, err := DatabaseURL(getenv)
	if err != nil {
		errs = append(errs, err)
	}
	cfg.DatabaseURL = databaseURL

	levelText := valueOr(getenv, "LOG_LEVEL", defaultLogLevel)
	if err := cfg.LogLevel.UnmarshalText([]byte(levelText)); err != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL is invalid: %w", err))
	}

	cfg.AllowedOrigins = splitList(getenv("ALLOWED_ORIGINS"))
	if len(cfg.AllowedOrigins) == 0 {
		if cfg.Env == EnvProd {
			errs = append(errs, errors.New("ALLOWED_ORIGINS is required in prod"))
		} else {
			cfg.AllowedOrigins = []string{defaultDevOrigin}
		}
	}

	cfg.RoomTTL, err = durationOr(getenv, "ROOM_TTL", defaultRoomTTL)
	if err != nil {
		errs = append(errs, err)
	}
	cfg.EmptyRoomTTL, err = durationOr(getenv, "EMPTY_ROOM_TTL", defaultEmptyRoomTTL)
	if err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("invalid configuration: %w", errors.Join(errs...))
	}
	return cfg, nil
}

func valueOr(getenv func(string) string, key, fallback string) string {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		return v
	}
	return fallback
}

func durationOr(getenv func(string) string, key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration such as 90m, got %q", key, raw)
	}
	return d, nil
}

func splitList(raw string) []string {
	var items []string
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}
