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

	insertRoom := func(code string, expiresAt time.Time, emptySince *time.Time) string {
		return insertID(t, db,
			"INSERT INTO rooms (code, expires_at, empty_since) VALUES (?, ?, ?) RETURNING id::text",
			code, expiresAt, emptySince)
	}
	emptyFor := func(d time.Duration) *time.Time {
		ts := now.Add(-d)
		return &ts
	}

	expiredID := insertRoom("OLDAA", now.Add(-time.Minute), nil)
	insertRoom("EMPTY", future, emptyFor(2*time.Hour))
	insertRoom("RECNT", future, emptyFor(30*time.Minute))
	insertRoom("ALIVE", future, nil)
	require.NoError(t, exec(db,
		"INSERT INTO players (room_id, name, token_hash) VALUES (?, 'Ana', 'h1')", expiredID))

	deleted, err := storage.NewRooms(db).DeleteExpired(ctx, now, time.Hour)

	require.NoError(t, err)
	assert.EqualValues(t, 2, deleted)

	var remaining []string
	require.NoError(t, db.NewRaw("SELECT code FROM rooms ORDER BY code").Scan(ctx, &remaining))
	assert.Equal(t, []string{"ALIVE", "RECNT"}, remaining)

	var orphanPlayers int
	require.NoError(t, db.NewRaw("SELECT count(*) FROM players").Scan(ctx, &orphanPlayers))
	assert.Zero(t, orphanPlayers, "players of a deleted room are removed by ON DELETE CASCADE")
}
