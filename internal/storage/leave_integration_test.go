//go:build integration

package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vhrboliveira/impostor-game/internal/room"
	"github.com/vhrboliveira/impostor-game/internal/storage"
)

func TestRooms_RemovePlayer(t *testing.T) {
	ctx := context.Background()
	lr := newLiveRoom(t)

	// The host leaves: Bia takes over in the same transaction.
	require.NoError(t, lr.rooms.RemovePlayer(ctx, lr.roomID, lr.ana, lr.bia, lobbyNow))

	players, err := lr.rooms.LoadPlayers(ctx, lr.roomID, matchID(t, lr))
	require.NoError(t, err)
	require.Len(t, players, 2, "who left stays, so the names of the round resolve")
	assert.False(t, players[0].Active())
	assert.False(t, players[0].IsHost)
	assert.True(t, players[1].Active())
	assert.True(t, players[1].IsHost)

	t.Run("their token no longer opens the room", func(t *testing.T) {
		_, _, err := lr.rooms.Authenticate(ctx, "ABCDE", "host-hash", lobbyNow)
		require.ErrorIs(t, err, room.ErrUnauthorized)
	})

	t.Run("their name is free again", func(t *testing.T) {
		_, err := lr.rooms.AddPlayer(ctx, addParams("ABCDE", "Ana"))
		require.NoError(t, err)
	})

	t.Run("twice is an error", func(t *testing.T) {
		require.Error(t, lr.rooms.RemovePlayer(ctx, lr.roomID, lr.ana, "", lobbyNow))
	})

	t.Run("the database keeps whoever left from being the host", func(t *testing.T) {
		_, err := lr.db.NewUpdate().Model((*storage.Player)(nil)).
			Set("is_host = true").Where("id = ?", lr.ana).Exec(ctx)
		require.Error(t, err)
	})
}
