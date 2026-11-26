//go:build integration

package storage_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"

	"github.com/vhrboliveira/impostor-game/internal/storage"
)

// schemaFixture builds the rows the constraint tests need, one model at a time.
type schemaFixture struct {
	t  *testing.T
	db *bun.DB
}

func (f schemaFixture) room() *storage.Room {
	r := &storage.Room{Code: "ABCDE", ExpiresAt: time.Now().Add(24 * time.Hour)}
	insert(f.t, f.db, r)
	return r
}

func (f schemaFixture) player(roomID, name string) *storage.Player {
	p := &storage.Player{RoomID: roomID, Name: name, TokenHash: "hash-" + name}
	insert(f.t, f.db, p)
	return p
}

func (f schemaFixture) match(roomID string) *storage.Match {
	m := &storage.Match{RoomID: roomID, Number: 1, TotalRounds: 3}
	insert(f.t, f.db, m)
	return m
}

func (f schemaFixture) theme(slug, kind string) *storage.Theme {
	th := &storage.Theme{Slug: slug, Name: slug, Kind: kind}
	insert(f.t, f.db, th)
	return th
}

func (f schemaFixture) word(themeID int64, text string) *storage.Word {
	w := &storage.Word{ThemeID: themeID, Text: text}
	insert(f.t, f.db, w)
	return w
}

// These tests prove that the database itself enforces the rules the game relies on.
func TestSchemaConstraints(t *testing.T) {
	setup := func(t *testing.T) schemaFixture {
		return schemaFixture{t: t, db: newMigratedDB(t)}
	}

	t.Run("words cannot belong to a players theme", func(t *testing.T) {
		f := setup(t)
		players := f.theme("p", "players")

		err := tryInsert(f.db, &storage.Word{ThemeID: players.ID, Text: "Ana"})

		require.Error(t, err)
	})

	t.Run("generic questions cannot be duplicated", func(t *testing.T) {
		f := setup(t)
		insert(t, f.db, &storage.Question{Kind: "words", Text: "Mesma pergunta?"})

		err := tryInsert(f.db, &storage.Question{Kind: "words", Text: "Mesma pergunta?"})

		require.Error(t, err)
	})

	t.Run("a question kind must match its theme kind", func(t *testing.T) {
		f := setup(t)
		foods := f.theme("f", "words")

		err := tryInsert(f.db, &storage.Question{Kind: "players", ThemeID: &foods.ID, Text: "Pergunta"})

		require.Error(t, err)
	})

	t.Run("a room has at most one host", func(t *testing.T) {
		f := setup(t)
		r := f.room()
		insert(t, f.db, &storage.Player{RoomID: r.ID, Name: "Ana", IsHost: true, TokenHash: "h1"})

		err := tryInsert(f.db, &storage.Player{RoomID: r.ID, Name: "Bia", IsHost: true, TokenHash: "h2"})

		require.Error(t, err)
	})

	t.Run("player names are unique within a room", func(t *testing.T) {
		f := setup(t)
		r := f.room()
		f.player(r.ID, "Ana")

		err := tryInsert(f.db, &storage.Player{RoomID: r.ID, Name: "Ana", TokenHash: "h2"})

		require.Error(t, err)
	})

	t.Run("matches only allow the supported numbers of rounds", func(t *testing.T) {
		f := setup(t)
		r := f.room()

		err := tryInsert(f.db, &storage.Match{RoomID: r.ID, Number: 1, TotalRounds: 4})

		require.Error(t, err)
		require.NoError(t, tryInsert(f.db, &storage.Match{RoomID: r.ID, Number: 1, TotalRounds: 5}))
	})

	t.Run("a player cannot vote for themselves", func(t *testing.T) {
		f := setup(t)
		r := f.room()
		ana := f.player(r.ID, "Ana")
		round := &storage.Round{MatchID: f.match(r.ID).ID, Number: 1}
		insert(t, f.db, round)

		err := tryInsert(f.db, &storage.Vote{RoundID: round.ID, VoterID: ana.ID, TargetID: &ana.ID})

		require.Error(t, err)
	})

	t.Run("a round cannot have both a word and a player as its secret", func(t *testing.T) {
		f := setup(t)
		r := f.room()
		ana := f.player(r.ID, "Ana")
		foods := f.theme("f", "words")
		pizza := f.word(foods.ID, "Pizza")

		err := tryInsert(f.db, &storage.Round{
			MatchID: f.match(r.ID).ID, Number: 1,
			ThemeID: &foods.ID, WordID: &pizza.ID, SubjectPlayerID: &ana.ID,
		})

		require.Error(t, err)
	})

	t.Run("a round word must belong to the round theme", func(t *testing.T) {
		f := setup(t)
		r := f.room()
		foods := f.theme("f", "words")
		animals := f.theme("a", "words")
		pizza := f.word(foods.ID, "Pizza")

		err := tryInsert(f.db, &storage.Round{
			MatchID: f.match(r.ID).ID, Number: 1, ThemeID: &animals.ID, WordID: &pizza.ID,
		})

		require.Error(t, err)
	})
}
