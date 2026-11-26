//go:build integration

package storage_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"

	"github.com/vhrboliveira/impostor-game/internal/room"
	"github.com/vhrboliveira/impostor-game/internal/storage"
)

var lobbyNow = time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

func createParams(code string) room.CreateRoomParams {
	return room.CreateRoomParams{
		Code:        code,
		Now:         lobbyNow,
		ExpiresAt:   lobbyNow.Add(24 * time.Hour),
		TotalRounds: 5,
		TurnSeconds: 180,
		HostName:    "Ana",
		TokenHash:   "host-hash",
	}
}

func addParams(code, name string) room.AddPlayerParams {
	return room.AddPlayerParams{
		Code:       code,
		Name:       name,
		TokenHash:  "hash-" + name,
		MaxPlayers: 20,
		Now:        lobbyNow.Add(time.Minute),
	}
}

// setMatchStatus changes the status of every match of the room.
func setMatchStatus(t *testing.T, db *bun.DB, roomID, status string) {
	t.Helper()
	exec(t, db.NewUpdate().Model((*storage.Match)(nil)).Set("status = ?", status).Where("room_id = ?", roomID))
}

// latestMatch reads the match of the room with the highest number.
func latestMatch(t *testing.T, db *bun.DB, roomID string) storage.Match {
	t.Helper()
	var m storage.Match
	require.NoError(t, db.NewSelect().Model(&m).Where("room_id = ?", roomID).Order("number DESC").Limit(1).Scan(context.Background()))
	return m
}

func TestRooms_CreateRoom(t *testing.T) {
	ctx := context.Background()

	t.Run("writes the room, its first match in the lobby and the host", func(t *testing.T) {
		db := newMigratedDB(t)
		rooms := storage.NewRooms(db)

		host, err := rooms.CreateRoom(ctx, createParams("ABCDE"))
		require.NoError(t, err)

		assert.NotEmpty(t, host.ID)
		assert.NotEmpty(t, host.RoomID)
		assert.Equal(t, "Ana", host.Name)
		assert.True(t, host.IsHost)

		var saved storage.Room
		require.NoError(t, db.NewSelect().Model(&saved).Where("id = ?", host.RoomID).Scan(ctx))
		assert.Equal(t, "ABCDE", saved.Code)
		assert.True(t, saved.ExpiresAt.Equal(lobbyNow.Add(24*time.Hour)))
		require.NotNil(t, saved.EmptySince)
		assert.True(t, saved.EmptySince.Equal(lobbyNow), "nobody is connected yet, so the room starts empty")

		match := latestMatch(t, db, host.RoomID)
		assert.Equal(t, 1, match.Number)
		assert.Equal(t, "lobby", match.Status)
		assert.Equal(t, 5, match.TotalRounds)
		assert.Equal(t, 180, match.TurnSeconds)

		var player storage.Player
		require.NoError(t, db.NewSelect().Model(&player).Where("id = ?", host.ID).Scan(ctx))
		assert.True(t, player.IsHost)
		assert.Equal(t, "host-hash", player.TokenHash)
	})

	t.Run("a code in use returns ErrCodeTaken and leaves no rows behind", func(t *testing.T) {
		db := newMigratedDB(t)
		rooms := storage.NewRooms(db)
		_, err := rooms.CreateRoom(ctx, createParams("ABCDE"))
		require.NoError(t, err)

		second := createParams("ABCDE")
		second.HostName = "Bia"
		_, err = rooms.CreateRoom(ctx, second)

		require.ErrorIs(t, err, room.ErrCodeTaken)
		assert.Equal(t, 1, count(t, db, (*storage.Room)(nil)))
		assert.Equal(t, 1, count(t, db, (*storage.Match)(nil)))
		assert.Equal(t, 1, count(t, db, (*storage.Player)(nil)))
	})
}

func TestRooms_AddPlayer(t *testing.T) {
	ctx := context.Background()

	// setup creates room ABCDE with its host and returns the room id.
	setup := func(t *testing.T) (*bun.DB, *storage.Rooms, string) {
		t.Helper()
		db := newMigratedDB(t)
		rooms := storage.NewRooms(db)
		host, err := rooms.CreateRoom(ctx, createParams("ABCDE"))
		require.NoError(t, err)
		return db, rooms, host.RoomID
	}

	// startRound puts the room's match in progress with one round in the given phase.
	startRound := func(t *testing.T, db *bun.DB, roomID, phase string) {
		t.Helper()
		setMatchStatus(t, db, roomID, "in_progress")
		insert(t, db, &storage.Round{MatchID: latestMatch(t, db, roomID).ID, Number: 1, Phase: phase})
	}

	t.Run("adds a player that is not the host", func(t *testing.T) {
		db, rooms, roomID := setup(t)

		player, err := rooms.AddPlayer(ctx, addParams("ABCDE", "Bia"))

		require.NoError(t, err)
		assert.NotEmpty(t, player.ID)
		assert.Equal(t, roomID, player.RoomID)
		assert.Equal(t, "Bia", player.Name)
		assert.False(t, player.IsHost)
		assert.Equal(t, 2, count(t, db, (*storage.Player)(nil)))
	})

	t.Run("unknown code returns ErrRoomNotFound", func(t *testing.T) {
		_, rooms, _ := setup(t)

		_, err := rooms.AddPlayer(ctx, addParams("ZZZZZ", "Bia"))

		require.ErrorIs(t, err, room.ErrRoomNotFound)
	})

	t.Run("expired room returns ErrRoomNotFound", func(t *testing.T) {
		_, rooms, _ := setup(t)
		params := addParams("ABCDE", "Bia")
		params.Now = lobbyNow.Add(25 * time.Hour)

		_, err := rooms.AddPlayer(ctx, params)

		require.ErrorIs(t, err, room.ErrRoomNotFound)
	})

	t.Run("name in use returns ErrNameTaken", func(t *testing.T) {
		db, rooms, _ := setup(t)

		_, err := rooms.AddPlayer(ctx, addParams("ABCDE", "Ana"))

		require.ErrorIs(t, err, room.ErrNameTaken)
		assert.Equal(t, 1, count(t, db, (*storage.Player)(nil)))
	})

	t.Run("full room returns ErrRoomFull", func(t *testing.T) {
		_, rooms, _ := setup(t)
		params := addParams("ABCDE", "Bia")
		params.MaxPlayers = 1

		_, err := rooms.AddPlayer(ctx, params)

		require.ErrorIs(t, err, room.ErrRoomFull)
	})

	phases := []struct {
		phase   string
		wantErr error
	}{
		{phase: "theme_selection", wantErr: room.ErrMatchInProgress},
		{phase: "reveal", wantErr: room.ErrMatchInProgress},
		{phase: "questions", wantErr: room.ErrMatchInProgress},
		{phase: "discussion", wantErr: room.ErrMatchInProgress},
		{phase: "voting", wantErr: room.ErrMatchInProgress},
		{phase: "result", wantErr: nil},
	}
	for _, tc := range phases {
		t.Run("match in progress, round in "+tc.phase, func(t *testing.T) {
			db, rooms, roomID := setup(t)
			startRound(t, db, roomID, tc.phase)

			_, err := rooms.AddPlayer(ctx, addParams("ABCDE", "Bia"))

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}

	t.Run("match in progress before its first round returns ErrMatchInProgress", func(t *testing.T) {
		db, rooms, roomID := setup(t)
		setMatchStatus(t, db, roomID, "in_progress")

		_, err := rooms.AddPlayer(ctx, addParams("ABCDE", "Bia"))

		require.ErrorIs(t, err, room.ErrMatchInProgress)
	})

	for _, status := range []string{"finished", "ended_early"} {
		t.Run("match "+status+" returns ErrMatchFinished", func(t *testing.T) {
			db, rooms, roomID := setup(t)
			setMatchStatus(t, db, roomID, status)

			_, err := rooms.AddPlayer(ctx, addParams("ABCDE", "Bia"))

			require.ErrorIs(t, err, room.ErrMatchFinished)
		})
	}

	t.Run("only the latest match counts", func(t *testing.T) {
		db, rooms, roomID := setup(t)
		startRound(t, db, roomID, "voting")
		setMatchStatus(t, db, roomID, "finished")
		insert(t, db, &storage.Match{RoomID: roomID, Number: 2, TotalRounds: 3})

		_, err := rooms.AddPlayer(ctx, addParams("ABCDE", "Bia"))

		require.NoError(t, err, "the new match is in the lobby")
	})

	t.Run("concurrent joins never exceed the player limit", func(t *testing.T) {
		db, rooms, _ := setup(t)
		const (
			maxPlayers = 5
			joiners    = 10
		)

		var (
			wg       sync.WaitGroup
			mu       sync.Mutex
			joined   int
			rejected int
		)
		for i := range joiners {
			wg.Go(func() {
				params := addParams("ABCDE", fmt.Sprintf("Player %d", i))
				params.MaxPlayers = maxPlayers
				_, err := rooms.AddPlayer(ctx, params)

				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					joined++
				case assert.ErrorIs(t, err, room.ErrRoomFull):
					rejected++
				}
			})
		}
		wg.Wait()

		assert.Equal(t, maxPlayers-1, joined, "the host already takes one seat")
		assert.Equal(t, joiners-(maxPlayers-1), rejected)
		assert.Equal(t, maxPlayers, count(t, db, (*storage.Player)(nil)))
	})
}
