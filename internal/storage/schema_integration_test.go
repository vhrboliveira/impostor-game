//go:build integration

package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func insertID(t *testing.T, db *bun.DB, query string, args ...any) string {
	t.Helper()

	var id string
	require.NoError(t, db.NewRaw(query, args...).Scan(context.Background(), &id))
	return id
}

func exec(db *bun.DB, query string, args ...any) error {
	_, err := db.ExecContext(context.Background(), query, args...)
	return err
}

// These tests prove that the database itself enforces the rules the game relies on.
func TestSchemaConstraints(t *testing.T) {
	t.Run("words cannot belong to a players theme", func(t *testing.T) {
		db := newMigratedDB(t)
		themeID := insertID(t, db, "INSERT INTO themes (slug, name, kind) VALUES ('p', 'Pessoas', 'players') RETURNING id::text")

		err := exec(db, "INSERT INTO words (theme_id, text) VALUES (?, 'Ana')", themeID)

		require.Error(t, err)
	})

	t.Run("generic questions cannot be duplicated", func(t *testing.T) {
		db := newMigratedDB(t)
		require.NoError(t, exec(db, "INSERT INTO questions (kind, text) VALUES ('words', 'Mesma pergunta?')"))

		err := exec(db, "INSERT INTO questions (kind, text) VALUES ('words', 'Mesma pergunta?')")

		require.Error(t, err)
	})

	t.Run("a question kind must match its theme kind", func(t *testing.T) {
		db := newMigratedDB(t)
		themeID := insertID(t, db, "INSERT INTO themes (slug, name, kind) VALUES ('f', 'Comidas', 'words') RETURNING id::text")

		err := exec(db, "INSERT INTO questions (kind, theme_id, text) VALUES ('players', ?, 'Pergunta')", themeID)

		require.Error(t, err)
	})

	t.Run("a room has at most one host", func(t *testing.T) {
		db := newMigratedDB(t)
		roomID := insertID(t, db, "INSERT INTO rooms (code, expires_at) VALUES ('ABCDE', now() + interval '24 hours') RETURNING id::text")
		require.NoError(t, exec(db, "INSERT INTO players (room_id, name, is_host, token_hash) VALUES (?, 'Ana', true, 'h1')", roomID))

		err := exec(db, "INSERT INTO players (room_id, name, is_host, token_hash) VALUES (?, 'Bia', true, 'h2')", roomID)

		require.Error(t, err)
	})

	t.Run("player names are unique within a room", func(t *testing.T) {
		db := newMigratedDB(t)
		roomID := insertID(t, db, "INSERT INTO rooms (code, expires_at) VALUES ('ABCDE', now() + interval '24 hours') RETURNING id::text")
		require.NoError(t, exec(db, "INSERT INTO players (room_id, name, token_hash) VALUES (?, 'Ana', 'h1')", roomID))

		err := exec(db, "INSERT INTO players (room_id, name, token_hash) VALUES (?, 'Ana', 'h2')", roomID)

		require.Error(t, err)
	})

	t.Run("matches only allow the supported numbers of rounds", func(t *testing.T) {
		db := newMigratedDB(t)
		roomID := insertID(t, db, "INSERT INTO rooms (code, expires_at) VALUES ('ABCDE', now() + interval '24 hours') RETURNING id::text")

		err := exec(db, "INSERT INTO matches (room_id, number, total_rounds) VALUES (?, 1, 4)", roomID)

		require.Error(t, err)
		require.NoError(t, exec(db, "INSERT INTO matches (room_id, number, total_rounds) VALUES (?, 1, 5)", roomID))
	})

	t.Run("a player cannot vote for themselves", func(t *testing.T) {
		db := newMigratedDB(t)
		roomID := insertID(t, db, "INSERT INTO rooms (code, expires_at) VALUES ('ABCDE', now() + interval '24 hours') RETURNING id::text")
		playerID := insertID(t, db, "INSERT INTO players (room_id, name, token_hash) VALUES (?, 'Ana', 'h1') RETURNING id::text", roomID)
		matchID := insertID(t, db, "INSERT INTO matches (room_id, number, total_rounds) VALUES (?, 1, 3) RETURNING id::text", roomID)
		roundID := insertID(t, db, "INSERT INTO rounds (match_id, number) VALUES (?, 1) RETURNING id::text", matchID)

		err := exec(db, "INSERT INTO votes (round_id, voter_id, target_id) VALUES (?, ?, ?)", roundID, playerID, playerID)

		require.Error(t, err)
	})

	t.Run("a round cannot have both a word and a player as its secret", func(t *testing.T) {
		db := newMigratedDB(t)
		roomID := insertID(t, db, "INSERT INTO rooms (code, expires_at) VALUES ('ABCDE', now() + interval '24 hours') RETURNING id::text")
		playerID := insertID(t, db, "INSERT INTO players (room_id, name, token_hash) VALUES (?, 'Ana', 'h1') RETURNING id::text", roomID)
		matchID := insertID(t, db, "INSERT INTO matches (room_id, number, total_rounds) VALUES (?, 1, 3) RETURNING id::text", roomID)
		themeID := insertID(t, db, "INSERT INTO themes (slug, name, kind) VALUES ('f', 'Comidas', 'words') RETURNING id::text")
		wordID := insertID(t, db, "INSERT INTO words (theme_id, text) VALUES (?, 'Pizza') RETURNING id::text", themeID)

		err := exec(db,
			"INSERT INTO rounds (match_id, number, theme_id, word_id, subject_player_id) VALUES (?, 1, ?, ?, ?)",
			matchID, themeID, wordID, playerID)

		require.Error(t, err)
	})

	t.Run("a round word must belong to the round theme", func(t *testing.T) {
		db := newMigratedDB(t)
		roomID := insertID(t, db, "INSERT INTO rooms (code, expires_at) VALUES ('ABCDE', now() + interval '24 hours') RETURNING id::text")
		matchID := insertID(t, db, "INSERT INTO matches (room_id, number, total_rounds) VALUES (?, 1, 3) RETURNING id::text", roomID)
		foodID := insertID(t, db, "INSERT INTO themes (slug, name, kind) VALUES ('f', 'Comidas', 'words') RETURNING id::text")
		animalID := insertID(t, db, "INSERT INTO themes (slug, name, kind) VALUES ('a', 'Animais', 'words') RETURNING id::text")
		pizzaID := insertID(t, db, "INSERT INTO words (theme_id, text) VALUES (?, 'Pizza') RETURNING id::text", foodID)

		err := exec(db, "INSERT INTO rounds (match_id, number, theme_id, word_id) VALUES (?, 1, ?, ?)", matchID, animalID, pizzaID)

		require.Error(t, err)
	})
}
