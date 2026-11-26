//go:build integration

package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vhrboliveira/impostor-game/internal/storage"
)

func TestRooms_DeleteExpired(t *testing.T) {
	ctx := context.Background()
	db := newMigratedDB(t)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	future := now.Add(10 * time.Hour)
	emptyFor := func(d time.Duration) *time.Time {
		ts := now.Add(-d)
		return &ts
	}

	expired := &storage.Room{Code: "OLDAA", ExpiresAt: now.Add(-time.Minute)}
	insert(t, db,
		expired,
		&storage.Room{Code: "EMPTY", ExpiresAt: future, EmptySince: emptyFor(2 * time.Hour)},
		&storage.Room{Code: "RECNT", ExpiresAt: future, EmptySince: emptyFor(30 * time.Minute)},
		&storage.Room{Code: "ALIVE", ExpiresAt: future},
	)
	insert(t, db, &storage.Player{RoomID: expired.ID, Name: "Ana", TokenHash: "h1"})

	deleted, err := storage.NewRooms(db).DeleteExpired(ctx, now, time.Hour)

	require.NoError(t, err)
	assert.EqualValues(t, 2, deleted)

	var remaining []string
	require.NoError(t, db.NewSelect().Model((*storage.Room)(nil)).Column("code").Order("code").Scan(ctx, &remaining))
	assert.Equal(t, []string{"ALIVE", "RECNT"}, remaining)
	assert.Zero(t, count(t, db, (*storage.Player)(nil)), "players of a deleted room are removed by ON DELETE CASCADE")
}

func TestRooms_DeleteExpired_CascadesWithMatchesAndRounds(t *testing.T) {
	ctx := context.Background()
	db := newMigratedDB(t)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

	room := &storage.Room{Code: "ROOM1", ExpiresAt: now.Add(-time.Minute)}
	insert(t, db, room)

	player1 := &storage.Player{RoomID: room.ID, Name: "Ana", TokenHash: "h1"}
	player2 := &storage.Player{RoomID: room.ID, Name: "Beto", TokenHash: "h2"}
	insert(t, db, player1, player2)

	match := &storage.Match{RoomID: room.ID, Number: 1, TotalRounds: 3}
	insert(t, db, match)

	round := &storage.Round{
		MatchID:          match.ID,
		Number:           1,
		ImpostorPlayerID: &player1.ID,
		SubjectPlayerID:  &player2.ID,
	}
	insert(t, db, round)

	turn := &storage.Turn{
		RoundID:    round.ID,
		Position:   0,
		AskerID:    player1.ID,
		AnswererID: player2.ID,
		QuestionID: 1,
	}
	// Insert question first to satisfy FK
	var question storage.Question
	err := db.NewSelect().Model(&question).Limit(1).Scan(ctx)
	if err == nil {
		turn.QuestionID = question.ID
		insert(t, db, turn)
	}

	vote := &storage.Vote{
		RoundID:  round.ID,
		VoterID:  player1.ID,
		TargetID: &player2.ID,
	}
	insert(t, db, vote)

	score := &storage.RoundScore{
		RoundID:  round.ID,
		PlayerID: player1.ID,
		Reason:   "impostor_escaped",
		Points:   2,
	}
	insert(t, db, score)

	deleted, err := storage.NewRooms(db).DeleteExpired(ctx, now, time.Hour)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)

	assert.Zero(t, count(t, db, (*storage.Room)(nil)))
	assert.Zero(t, count(t, db, (*storage.Player)(nil)))
	assert.Zero(t, count(t, db, (*storage.Match)(nil)))
	assert.Zero(t, count(t, db, (*storage.Round)(nil)))
	assert.Zero(t, count(t, db, (*storage.Vote)(nil)))
	assert.Zero(t, count(t, db, (*storage.RoundScore)(nil)))
}
