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

	deleted, err := storage.NewRooms(db).DeleteExpired(ctx, now, time.Hour, 100)

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

	deleted, err := storage.NewRooms(db).DeleteExpired(ctx, now, time.Hour, 100)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)

	assert.Zero(t, count(t, db, (*storage.Room)(nil)))
	assert.Zero(t, count(t, db, (*storage.Player)(nil)))
	assert.Zero(t, count(t, db, (*storage.Match)(nil)))
	assert.Zero(t, count(t, db, (*storage.Round)(nil)))
	assert.Zero(t, count(t, db, (*storage.Vote)(nil)))
	assert.Zero(t, count(t, db, (*storage.RoundScore)(nil)))
}

func TestRooms_DeleteExpired_InBatches(t *testing.T) {
	ctx := context.Background()
	db := newMigratedDB(t)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	for _, code := range []string{"OLDAA", "OLDBB", "OLDCC"} {
		insert(t, db, &storage.Room{Code: code, ExpiresAt: now.Add(-time.Minute)})
	}
	rooms := storage.NewRooms(db)

	deleted, err := rooms.DeleteExpired(ctx, now, time.Hour, 2)
	require.NoError(t, err)
	assert.EqualValues(t, 2, deleted)
	deleted, err = rooms.DeleteExpired(ctx, now, time.Hour, 2)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)
	assert.Zero(t, count(t, db, (*storage.Room)(nil)))
}

// After a restart no WebSocket is open, so a room still marked occupied (a crash or an interrupted
// shutdown left it so) starts its empty-room TTL.
func TestRooms_MarkAllEmpty(t *testing.T) {
	ctx := context.Background()
	db := newMigratedDB(t)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	earlier := now.Add(-10 * time.Minute)
	insert(t, db,
		&storage.Room{Code: "BUSYY", ExpiresAt: now.Add(time.Hour)},
		&storage.Room{Code: "EMPTY", ExpiresAt: now.Add(time.Hour), EmptySince: &earlier},
	)

	require.NoError(t, storage.NewRooms(db).MarkAllEmpty(ctx, now))

	var rows []storage.Room
	require.NoError(t, db.NewSelect().Model(&rows).Order("code").Scan(ctx))
	require.Len(t, rows, 2)
	require.NotNil(t, rows[0].EmptySince)
	assert.True(t, rows[0].EmptySince.Equal(now), "the occupied marker is stale after a restart")
	require.NotNil(t, rows[1].EmptySince)
	assert.True(t, rows[1].EmptySince.Equal(earlier), "an empty room keeps its time")
}
