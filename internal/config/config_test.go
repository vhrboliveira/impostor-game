package config

import (
	"log/slog"
	"net/netip"
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
		"LOG_LEVEL":       "warn",
		"ALLOWED_ORIGINS": " https://a.example , https://b.example ,",
		"TRUSTED_PROXIES": "10.0.0.0/8, 192.168.1.10",
		"ROOM_TTL":        "12h",
		"EMPTY_ROOM_TTL":  "30m",
	}))

	require.NoError(t, err)
	assert.True(t, cfg.IsProd())
	assert.Equal(t, "9000", cfg.Port)
	assert.Equal(t, slog.LevelWarn, cfg.LogLevel)
	assert.Equal(t, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.168.1.10/32")}, cfg.TrustedProxies)
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

// LOG_LEVEL=debug logs every SQL query with its values (photos, token hashes, who the impostor
// is): never in production.
func TestLoad_RefusesDebugLogsInProd(t *testing.T) {
	_, err := Load(env(map[string]string{
		"APP_ENV": "prod", "DATABASE_URL": "postgres://db", "ALLOWED_ORIGINS": "https://a.example", "LOG_LEVEL": "debug",
	}))
	require.Error(t, err)
	assert.ErrorContains(t, err, "LOG_LEVEL")

	cfg, err := Load(env(map[string]string{"DATABASE_URL": "postgres://db", "LOG_LEVEL": "debug"}))
	require.NoError(t, err, "debug is fine in dev")
	assert.Equal(t, slog.LevelDebug, cfg.LogLevel)
}

// A malformed DATABASE_URL is reported without its password (it used to reach the logs through
// a panic of the driver).
func TestLoad_InvalidDatabaseURLHidesThePassword(t *testing.T) {
	_, err := Load(env(map[string]string{"DATABASE_URL": "postgres://user:S3cr%zzet@db:5432/app"}))
	require.Error(t, err)
	assert.ErrorContains(t, err, "DATABASE_URL")
	assert.NotContains(t, err.Error(), "S3cr")

	_, err = DatabaseURL(env(map[string]string{"DATABASE_URL": "not a url at all"}))
	assert.Error(t, err)
}

func TestLoad_ReportsInvalidTrustedProxies(t *testing.T) {
	_, err := Load(env(map[string]string{"DATABASE_URL": "postgres://db", "TRUSTED_PROXIES": "10.0.0.0/8, nope"}))
	require.Error(t, err)
	assert.ErrorContains(t, err, "TRUSTED_PROXIES")
}

// In production the database connection must verify the server certificate; anything else is
// reported so the operator sees it at startup.
func TestConfig_InsecureDatabaseTLS(t *testing.T) {
	for url, insecure := range map[string]bool{
		"postgres://u:p@db/app?sslmode=verify-full": false,
		"postgres://u:p@db/app?sslmode=require":     true,
		"postgres://u:p@db/app":                     true,
		"postgres://u:p@db/app?sslmode=disable":     true,
	} {
		assert.Equal(t, insecure, Config{DatabaseURL: url}.InsecureDatabaseTLS(), url)
	}
}
