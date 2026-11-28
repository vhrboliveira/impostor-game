package content

import (
	"fmt"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vhrboliveira/impostor-game/internal/game"
)

func validCatalog() Catalog {
	return Catalog{
		Themes: []Theme{
			{Slug: "foods", Name: "Comidas", Kind: game.ThemeKindWords, Words: []string{"Pizza", "Sushi"}},
			{Slug: "players", Name: "Pessoas da sala", Kind: game.ThemeKindPlayers},
		},
		Questions: []Question{
			{Kind: game.ThemeKindWords, Text: "Você levaria isso numa viagem?"},
			{Kind: game.ThemeKindWords, ThemeSlug: "foods", Text: "Isso é doce ou salgado?"},
			{Kind: game.ThemeKindPlayers, Text: "Essa pessoa costuma chegar atrasada?"},
		},
	}
}

func TestLoad_EmbeddedCatalogIsValid(t *testing.T) {
	catalog, err := Load()

	require.NoError(t, err)
	assert.NotEmpty(t, catalog.Themes)
	assert.NotEmpty(t, catalog.Questions)
	assert.Positive(t, catalog.WordCount())
	require.NoError(t, catalog.Validate())
}

// The shipped catalog must meet the release minimums (enough questions per theme), so it is
// never published short of content.
func TestLoad_EmbeddedCatalogIsReadyForRelease(t *testing.T) {
	catalog, err := Load()
	require.NoError(t, err)

	require.NoError(t, catalog.ValidateForRelease())
}

func TestLoad_RejectsUnknownFields(t *testing.T) {
	fsys := fstest.MapFS{
		"data/themes.json":    {Data: []byte(`{"themes":[{"slug":"x","nam":"typo"}]}`)},
		"data/questions.json": {Data: []byte(`{"questions":[]}`)},
	}

	_, err := load(fsys)

	require.Error(t, err)
	assert.ErrorContains(t, err, "data/themes.json")
}

func TestLoad_FailsWhenAFileIsMissing(t *testing.T) {
	_, err := load(fstest.MapFS{})

	require.Error(t, err)
	assert.ErrorContains(t, err, "data/themes.json")
}

func TestValidate_AcceptsAValidCatalog(t *testing.T) {
	require.NoError(t, validCatalog().Validate())
}

func TestValidate_ReportsProblems(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(c *Catalog)
		want   string
	}{
		{
			name:   "invalid slug",
			mutate: func(c *Catalog) { c.Themes[0].Slug = "Foods Now" },
			want:   "slug must be",
		},
		{
			name:   "duplicated slug",
			mutate: func(c *Catalog) { c.Themes[1].Slug = "foods" },
			want:   "duplicated slug",
		},
		{
			name:   "empty name",
			mutate: func(c *Catalog) { c.Themes[0].Name = " " },
			want:   "name is required",
		},
		{
			name:   "invalid kind",
			mutate: func(c *Catalog) { c.Themes[0].Kind = "other" },
			want:   "invalid kind",
		},
		{
			name:   "words theme without words",
			mutate: func(c *Catalog) { c.Themes[0].Words = nil },
			want:   "need at least one word",
		},
		{
			name:   "players theme with words",
			mutate: func(c *Catalog) { c.Themes[1].Words = []string{"Ana"} },
			want:   "must not have words",
		},
		{
			name:   "duplicated word ignoring case",
			mutate: func(c *Catalog) { c.Themes[0].Words = []string{"Pizza", "pizza"} },
			want:   "duplicated word",
		},
		{
			name:   "word with surrounding spaces",
			mutate: func(c *Catalog) { c.Themes[0].Words = []string{" Pizza"} },
			want:   "leading or trailing spaces",
		},
		{
			name:   "question for an unknown theme",
			mutate: func(c *Catalog) { c.Questions[1].ThemeSlug = "missing" },
			want:   "unknown theme",
		},
		{
			name:   "question kind differs from its theme",
			mutate: func(c *Catalog) { c.Questions[1].Kind = game.ThemeKindPlayers },
			want:   "does not match theme",
		},
		{
			name:   "empty question",
			mutate: func(c *Catalog) { c.Questions[0].Text = "" },
			want:   "text is required",
		},
		{
			name:   "duplicated question",
			mutate: func(c *Catalog) { c.Questions = append(c.Questions, c.Questions[0]) },
			want:   "duplicated question",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := validCatalog()
			tt.mutate(&catalog)

			err := catalog.Validate()

			require.Error(t, err)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestValidateForRelease_RequiresEnoughQuestionsPerTheme(t *testing.T) {
	err := validCatalog().ValidateForRelease()

	require.Error(t, err)
	assert.ErrorContains(t, err, `theme "foods"`)
	assert.ErrorContains(t, err, `theme "players"`)
}

func TestValidateForRelease_AcceptsACatalogWithEnoughQuestions(t *testing.T) {
	catalog := validCatalog()
	for i := range game.MaxPlayers {
		catalog.Questions = append(catalog.Questions,
			Question{Kind: game.ThemeKindWords, Text: fmt.Sprintf("Pergunta de palavras %d", i)},
			Question{Kind: game.ThemeKindPlayers, Text: fmt.Sprintf("Pergunta de pessoas %d", i)},
		)
	}

	require.NoError(t, catalog.ValidateForRelease())
}
