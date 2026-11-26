// Package storagetest gives integration tests a real, freshly migrated PostgreSQL database. It
// is only imported by tests built with the integration tag.
package storagetest

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"

	"github.com/vhrboliveira/impostor-game/internal/storage"
	"github.com/vhrboliveira/impostor-game/internal/storage/migrations"
)

// DefaultDSN is used when TEST_DATABASE_URL is not set. It matches docker-compose.yml.
const DefaultDSN = "postgres://impostor:impostor@localhost:5436/impostor_test?sslmode=disable"

// NewTestDB connects to TEST_DATABASE_URL and gives the test a pristine, empty schema.
// Run these tests with `-p 1` (make test-integration): they share one database.
func NewTestDB(t *testing.T) *bun.DB {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = DefaultDSN
	}
	requireTestDatabase(t, dsn)

	ctx := context.Background()
	db, err := storage.Open(ctx, dsn, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err, "is Postgres running? try `make db-up`")
	t.Cleanup(func() { _ = db.Close() })

	// Schema DDL, like the migrations, is plain SQL: the query builder has no schema statements.
	_, err = db.ExecContext(ctx, "DROP SCHEMA IF EXISTS public CASCADE")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "CREATE SCHEMA public")
	require.NoError(t, err)

	return db
}

// NewMigrator returns a migrator with the bookkeeping tables created.
func NewMigrator(t *testing.T, db *bun.DB) *migrate.Migrator {
	t.Helper()

	migrator := migrate.NewMigrator(db, migrations.Migrations)
	require.NoError(t, migrator.Init(context.Background()))
	return migrator
}

// NewMigratedDB returns a test database with every migration applied.
func NewMigratedDB(t *testing.T) *bun.DB {
	t.Helper()

	db := NewTestDB(t)
	group, err := NewMigrator(t, db).Migrate(context.Background())
	require.NoError(t, err)
	require.False(t, group.IsZero(), "expected migrations to be applied")
	return db
}

// requireTestDatabase refuses to wipe a database whose name does not end with _test.
func requireTestDatabase(t *testing.T, dsn string) {
	t.Helper()

	u, err := url.Parse(dsn)
	require.NoError(t, err)
	name := strings.TrimPrefix(u.Path, "/")
	require.True(t, strings.HasSuffix(name, "_test"),
		"refusing to reset database %q: its name must end with _test", name)
}
