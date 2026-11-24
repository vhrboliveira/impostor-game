package storage

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"

	"github.com/vhrboliveira/impostor-game/internal/content"
)

// SeedCatalog writes the catalog to the database inside a single transaction. It is
// idempotent: existing themes only get their name refreshed, and words and questions that
// already exist are skipped. A theme's kind never changes once created.
func SeedCatalog(ctx context.Context, db *bun.DB, catalog content.Catalog) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		themeIDs := make(map[string]int64, len(catalog.Themes))

		for _, t := range catalog.Themes {
			theme := Theme{Slug: t.Slug, Name: t.Name, Kind: string(t.Kind)}
			if _, err := tx.NewInsert().
				Model(&theme).
				On("CONFLICT (slug) DO UPDATE").
				Set("name = EXCLUDED.name").
				Returning("id").
				Exec(ctx); err != nil {
				return fmt.Errorf("upsert theme %q: %w", t.Slug, err)
			}
			themeIDs[t.Slug] = theme.ID

			if len(t.Words) == 0 {
				continue
			}
			words := make([]Word, 0, len(t.Words))
			for _, text := range t.Words {
				words = append(words, Word{ThemeID: theme.ID, Text: text})
			}
			if _, err := tx.NewInsert().
				Model(&words).
				On("CONFLICT (theme_id, text) DO NOTHING").
				Exec(ctx); err != nil {
				return fmt.Errorf("insert words of theme %q: %w", t.Slug, err)
			}
		}

		if len(catalog.Questions) == 0 {
			return nil
		}
		questions := make([]Question, 0, len(catalog.Questions))
		for _, q := range catalog.Questions {
			var themeID *int64
			if q.ThemeSlug != "" {
				id, ok := themeIDs[q.ThemeSlug]
				if !ok {
					return fmt.Errorf("question %q references unknown theme %q", q.Text, q.ThemeSlug)
				}
				themeID = &id
			}
			questions = append(questions, Question{Kind: string(q.Kind), ThemeID: themeID, Text: q.Text})
		}
		if _, err := tx.NewInsert().
			Model(&questions).
			On("CONFLICT ON CONSTRAINT questions_kind_theme_text_key DO NOTHING").
			Exec(ctx); err != nil {
			return fmt.Errorf("insert questions: %w", err)
		}
		return nil
	})
}
