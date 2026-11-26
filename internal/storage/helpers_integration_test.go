//go:build integration

package storage_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"

	"github.com/vhrboliveira/impostor-game/internal/storage/storagetest"
)

// The helpers live in package storagetest so other packages' integration tests can use them.

func newTestDB(t *testing.T) *bun.DB { return storagetest.NewTestDB(t) }

func newMigrator(t *testing.T, db *bun.DB) *migrate.Migrator { return storagetest.NewMigrator(t, db) }

func newMigratedDB(t *testing.T) *bun.DB { return storagetest.NewMigratedDB(t) }

func listTables(t *testing.T, db *bun.DB) []string {
	t.Helper()

	var tables []string
	err := db.NewSelect().
		TableExpr("information_schema.tables").
		Column("table_name").
		Where("table_schema = 'public'").
		Where("table_name NOT LIKE 'bun_%'").
		Order("table_name").
		Scan(context.Background(), &tables)
	require.NoError(t, err)
	return tables
}

// The fixtures use the same models and query builder as the repositories, so the column names
// live in one place (models.go).

// insert writes each model (a pointer to a struct or to a slice of structs) and reads every
// column back, so ids and database defaults are filled in.
func insert(t *testing.T, db bun.IDB, models ...any) {
	t.Helper()
	for _, model := range models {
		_, err := db.NewInsert().Model(model).Returning("*").Exec(context.Background())
		require.NoError(t, err)
	}
}

// tryInsert writes one model and returns the database error, for the constraint tests.
func tryInsert(db bun.IDB, model any) error {
	_, err := db.NewInsert().Model(model).Exec(context.Background())
	return err
}

// count returns how many rows the table of model has, e.g. count(t, db, (*storage.Room)(nil)).
func count(t *testing.T, db bun.IDB, model any) int {
	t.Helper()
	n, err := db.NewSelect().Model(model).Count(context.Background())
	require.NoError(t, err)
	return int(n)
}

// exec runs an update or delete built with the query builder and fails the test on error.
func exec(t *testing.T, q interface {
	Exec(ctx context.Context, dest ...any) (sql.Result, error)
}) {
	t.Helper()
	_, err := q.Exec(context.Background())
	require.NoError(t, err)
}
