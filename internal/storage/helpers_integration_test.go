//go:build integration

package storage_test

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

const defaultTestDSN = "postgres://impostor:impostor@localhost:5436/impostor_test?sslmode=disable"

// newTestDB connects to TEST_DATABASE_URL and gives the test a pristine, empty schema.
// Run these tests with `-p 1` (make test-integration): they share one database.
func newTestDB(t *testing.T) *bun.DB {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = defaultTestDSN
	}
	requireTestDatabase(t, dsn)

	ctx := context.Background()
	db, err := storage.Open(ctx, dsn, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err, "is Postgres running? try `make db-up`")
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.ExecContext(ctx, "DROP SCHEMA IF EXISTS public CASCADE")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "CREATE SCHEMA public")
	require.NoError(t, err)

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

func newMigrator(t *testing.T, db *bun.DB) *migrate.Migrator {
	t.Helper()

	migrator := migrate.NewMigrator(db, migrations.Migrations)
	require.NoError(t, migrator.Init(context.Background()))
	return migrator
}

// newMigratedDB returns a test database with every migration applied.
func newMigratedDB(t *testing.T) *bun.DB {
	t.Helper()

	db := newTestDB(t)
	group, err := newMigrator(t, db).Migrate(context.Background())
	require.NoError(t, err)
	require.False(t, group.IsZero(), "expected migrations to be applied")
	return db
}

func listTables(t *testing.T, db *bun.DB) []string {
	t.Helper()

	var tables []string
	err := db.NewRaw(`SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name NOT LIKE 'bun_%'
		ORDER BY table_name`).Scan(context.Background(), &tables)
	require.NoError(t, err)
	return tables
}
