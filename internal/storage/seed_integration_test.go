//go:build integration

package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vhrboliveira/impostor-game/internal/content"
	"github.com/vhrboliveira/impostor-game/internal/storage"
)

func TestSeedCatalog_WritesTheCatalogAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := newMigratedDB(t)
	catalog, err := content.Load()
	require.NoError(t, err)

	for run := 1; run <= 2; run++ {
		require.NoError(t, storage.SeedCatalog(ctx, db, catalog), "run %d", run)

		themes, err := db.NewSelect().Model((*storage.Theme)(nil)).Count(ctx)
		require.NoError(t, err)
		words, err := db.NewSelect().Model((*storage.Word)(nil)).Count(ctx)
		require.NoError(t, err)
		questions, err := db.NewSelect().Model((*storage.Question)(nil)).Count(ctx)
		require.NoError(t, err)

		assert.EqualValues(t, len(catalog.Themes), themes, "themes after run %d", run)
		assert.EqualValues(t, catalog.WordCount(), words, "words after run %d", run)
		assert.EqualValues(t, len(catalog.Questions), questions, "questions after run %d", run)
	}
}

func TestSeedCatalog_KeepsGenericQuestionsWithoutATheme(t *testing.T) {
	ctx := context.Background()
	db := newMigratedDB(t)
	catalog, err := content.Load()
	require.NoError(t, err)
	require.NoError(t, storage.SeedCatalog(ctx, db, catalog))

	generic, err := db.NewSelect().Model((*storage.Question)(nil)).Where("theme_id IS NULL").Count(ctx)
	require.NoError(t, err)

	expected := 0
	for _, q := range catalog.Questions {
		if q.ThemeSlug == "" {
			expected++
		}
	}
	assert.EqualValues(t, expected, generic)
}
