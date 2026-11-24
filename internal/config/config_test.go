package config

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoad_AppliesDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"DATABASE_URL": "postgres://db"}))

	require.NoError(t, err)
	assert.Equal(t, EnvDev, cfg.Env)
	assert.Equal(t, "8080", cfg.Port)
	assert.Equal(t, "postgres://db", cfg.DatabaseURL)
	assert.Equal(t, slog.LevelInfo, cfg.LogLevel)
	assert.Equal(t, []string{"http://localhost:5173"}, cfg.AllowedOrigins)
	assert.Equal(t, 24*time.Hour, cfg.RoomTTL)
	assert.Equal(t, time.Hour, cfg.EmptyRoomTTL)
	assert.False(t, cfg.IsProd())
}

func TestLoad_ReadsExplicitValues(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"APP_ENV":         "prod",
		"PORT":            "9000",
		"DATABASE_URL":    "postgres://db",
		"LOG_LEVEL":       "debug",
		"ALLOWED_ORIGINS": " https://a.example , https://b.example ,",
		"ROOM_TTL":        "12h",
		"EMPTY_ROOM_TTL":  "30m",
	}))

	require.NoError(t, err)
	assert.True(t, cfg.IsProd())
	assert.Equal(t, "9000", cfg.Port)
	assert.Equal(t, slog.LevelDebug, cfg.LogLevel)
	assert.Equal(t, []string{"https://a.example", "https://b.example"}, cfg.AllowedOrigins)
	assert.Equal(t, 12*time.Hour, cfg.RoomTTL)
	assert.Equal(t, 30*time.Minute, cfg.EmptyRoomTTL)
}

func TestLoad_ReportsEveryProblemAtOnce(t *testing.T) {
	_, err := Load(env(map[string]string{
		"APP_ENV":        "staging",
		"PORT":           "99999",
		"LOG_LEVEL":      "loud",
		"ROOM_TTL":       "-1h",
		"EMPTY_ROOM_TTL": "soon",
	}))

	require.Error(t, err)
	for _, fragment := range []string{
		"APP_ENV",
		"PORT",
		"DATABASE_URL",
		"LOG_LEVEL",
		"ROOM_TTL",
		"EMPTY_ROOM_TTL",
	} {
		assert.ErrorContains(t, err, fragment)
	}
}

func TestLoad_RequiresAllowedOriginsInProd(t *testing.T) {
	_, err := Load(env(map[string]string{
		"APP_ENV":      "prod",
		"DATABASE_URL": "postgres://db",
	}))

	require.Error(t, err)
	assert.ErrorContains(t, err, "ALLOWED_ORIGINS")
}

func TestDatabaseURL(t *testing.T) {
	url, err := DatabaseURL(env(map[string]string{"DATABASE_URL": "postgres://db"}))
	require.NoError(t, err)
	assert.Equal(t, "postgres://db", url)

	_, err = DatabaseURL(env(nil))
	require.Error(t, err)
}
