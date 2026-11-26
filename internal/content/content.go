// Package content loads and validates the game catalog (themes, words and questions) that is
// embedded in the binary. The catalog lives in versioned JSON files and is written to the
// database by `cmd/seed`.
package content

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/vhrboliveira/impostor-game/internal/game"
)

//go:embed data/*.json
var dataFS embed.FS

// MaxTextLength is the maximum length of a word or question.
const MaxTextLength = 255

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Catalog is the full game content.
type Catalog struct {
	Themes    []Theme
	Questions []Question
}

// Theme is a theme players can pick for a round.
type Theme struct {
	Slug string         `json:"slug"`
	Name string         `json:"name"`
	Kind game.ThemeKind `json:"kind"`
	// Words is only used by "words" themes. "players" themes draw from the people in the room.
	Words []string `json:"words"`
}

// Question is a question read aloud during the turns of a round.
type Question struct {
	Kind game.ThemeKind `json:"kind"`
	// ThemeSlug is empty for generic questions, which fit every theme of the same kind.
	ThemeSlug string `json:"themeSlug"`
	Text      string `json:"text"`
}

// Load reads the embedded catalog.
func Load() (Catalog, error) {
	return load(dataFS)
}

func load(fsys fs.FS) (Catalog, error) {
	var themes struct {
		Themes []Theme `json:"themes"`
	}
	if err := decodeFile(fsys, "data/themes.json", &themes); err != nil {
		return Catalog{}, err
	}

	var questions struct {
		Questions []Question `json:"questions"`
	}
	if err := decodeFile(fsys, "data/questions.json", &questions); err != nil {
		return Catalog{}, err
	}

	return Catalog{Themes: themes.Themes, Questions: questions.Questions}, nil
}

func decodeFile(fsys fs.FS, name string, dst any) error {
	f, err := fsys.Open(name)
	if err != nil {
		return fmt.Errorf("open %s: %w", name, err)
	}
	defer func() { _ = f.Close() }() // read-only file: a close error loses nothing

	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("decode %s: %w", name, err)
	}
	return nil
}

// WordCount returns the total number of words in the catalog.
func (c Catalog) WordCount() int {
	total := 0
	for _, t := range c.Themes {
		total += len(t.Words)
	}
	return total
}

// Validate checks the structural rules of the catalog and reports every problem found.
func (c Catalog) Validate() error {
	var errs []error

	themeKinds := make(map[string]game.ThemeKind, len(c.Themes))
	for i, t := range c.Themes {
		where := fmt.Sprintf("themes[%d] %q", i, t.Slug)

		if !slugPattern.MatchString(t.Slug) {
			errs = append(errs, fmt.Errorf("%s: slug must be lowercase letters, digits and hyphens", where))
		}
		if _, duplicated := themeKinds[t.Slug]; duplicated {
			errs = append(errs, fmt.Errorf("%s: duplicated slug", where))
		}
		themeKinds[t.Slug] = t.Kind

		if strings.TrimSpace(t.Name) == "" {
			errs = append(errs, fmt.Errorf("%s: name is required", where))
		}
		if !t.Kind.Valid() {
			errs = append(errs, fmt.Errorf("%s: invalid kind %q", where, t.Kind))
		}
		errs = append(errs, validateWords(where, t)...)
	}

	seenQuestions := make(map[string]bool, len(c.Questions))
	for i, q := range c.Questions {
		where := fmt.Sprintf("questions[%d]", i)

		if !q.Kind.Valid() {
			errs = append(errs, fmt.Errorf("%s: invalid kind %q", where, q.Kind))
		}
		if err := validateText(q.Text); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", where, err))
		}
		if q.ThemeSlug != "" {
			kind, ok := themeKinds[q.ThemeSlug]
			switch {
			case !ok:
				errs = append(errs, fmt.Errorf("%s: unknown theme %q", where, q.ThemeSlug))
			case kind != q.Kind:
				errs = append(errs, fmt.Errorf("%s: kind %q does not match theme %q (%q)", where, q.Kind, q.ThemeSlug, kind))
			}
		}

		key := string(q.Kind) + "|" + q.ThemeSlug + "|" + normalize(q.Text)
		if seenQuestions[key] {
			errs = append(errs, fmt.Errorf("%s: duplicated question %q", where, q.Text))
		}
		seenQuestions[key] = true
	}

	return errors.Join(errs...)
}

// ValidateForRelease applies Validate plus the content minimums. Every theme must have at
// least game.MaxPlayers questions available (generic ones plus its own), because the questions
// of a round cannot repeat and a round has one turn per player.
func (c Catalog) ValidateForRelease() error {
	if err := c.Validate(); err != nil {
		return err
	}

	generic := make(map[game.ThemeKind]int)
	specific := make(map[string]int)
	for _, q := range c.Questions {
		if q.ThemeSlug == "" {
			generic[q.Kind]++
		} else {
			specific[q.ThemeSlug]++
		}
	}

	var errs []error
	for _, t := range c.Themes {
		available := generic[t.Kind] + specific[t.Slug]
		if available < game.MaxPlayers {
			errs = append(errs, fmt.Errorf("theme %q has %d questions available, need at least %d", t.Slug, available, game.MaxPlayers))
		}
	}
	return errors.Join(errs...)
}

func validateWords(where string, t Theme) []error {
	var errs []error

	switch {
	case t.Kind == game.ThemeKindPlayers && len(t.Words) > 0:
		errs = append(errs, fmt.Errorf("%s: players themes must not have words", where))
	case t.Kind == game.ThemeKindWords && len(t.Words) == 0:
		errs = append(errs, fmt.Errorf("%s: words themes need at least one word", where))
	}

	seen := make(map[string]bool, len(t.Words))
	for _, w := range t.Words {
		if err := validateText(w); err != nil {
			errs = append(errs, fmt.Errorf("%s: word %q: %w", where, w, err))
		}
		key := normalize(w)
		if seen[key] {
			errs = append(errs, fmt.Errorf("%s: duplicated word %q", where, w))
		}
		seen[key] = true
	}
	return errs
}

func validateText(text string) error {
	trimmed := strings.TrimSpace(text)
	switch {
	case trimmed == "":
		return errors.New("text is required")
	case len([]rune(trimmed)) > MaxTextLength:
		return fmt.Errorf("text is longer than %d characters", MaxTextLength)
	case trimmed != text:
		return errors.New("text has leading or trailing spaces")
	}
	return nil
}

func normalize(text string) string {
	return strings.ToLower(strings.TrimSpace(text))
}
