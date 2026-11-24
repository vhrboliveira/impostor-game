//go:build integration

package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var appTables = []string{
	"matches", "players", "questions", "round_scores", "rooms",
	"rounds", "themes", "turns", "votes", "words",
}

func TestMigrations_UpCreatesTheSchemaAndDownRemovesIt(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	migrator := newMigrator(t, db)

	group, err := migrator.Migrate(ctx)
	require.NoError(t, err)
	require.False(t, group.IsZero())
	assert.ElementsMatch(t, appTables, listTables(t, db))

	rolledBack, err := migrator.Rollback(ctx)
	require.NoError(t, err)
	require.False(t, rolledBack.IsZero())
	assert.Empty(t, listTables(t, db))
}

func TestMigrations_CanBeAppliedAgainAfterARollback(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	migrator := newMigrator(t, db)

	_, err := migrator.Migrate(ctx)
	require.NoError(t, err)
	_, err = migrator.Rollback(ctx)
	require.NoError(t, err)

	_, err = migrator.Migrate(ctx)

	require.NoError(t, err)
	assert.ElementsMatch(t, appTables, listTables(t, db))
}

func TestMigrations_UpIsANoOpWhenAlreadyApplied(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	migrator := newMigrator(t, db)
	_, err := migrator.Migrate(ctx)
	require.NoError(t, err)

	group, err := migrator.Migrate(ctx)

	require.NoError(t, err)
	assert.True(t, group.IsZero())
}
