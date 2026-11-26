//go:build integration

package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vhrboliveira/impostor-game/internal/room"
	"github.com/vhrboliveira/impostor-game/internal/storage"
)

func TestRooms_Avatars(t *testing.T) {
	ctx := context.Background()
	lr := newLiveRoom(t)
	first := lobbyNow.Truncate(time.Microsecond)

	_, err := lr.rooms.LoadAvatar(ctx, "ABCDE", lr.ana, lobbyNow)
	require.ErrorIs(t, err, room.ErrAvatarNotFound)

	require.NoError(t, lr.rooms.SetAvatar(ctx, lr.ana, []byte("first"), first))
	avatar, err := lr.rooms.LoadAvatar(ctx, "ABCDE", lr.ana, lobbyNow)
	require.NoError(t, err)
	assert.Equal(t, []byte("first"), avatar.Image)
	assert.True(t, first.Equal(avatar.UpdatedAt))

	// A new photo replaces the old one and its version.
	second := first.Add(time.Minute)
	require.NoError(t, lr.rooms.SetAvatar(ctx, lr.ana, []byte("second"), second))
	avatar, err = lr.rooms.LoadAvatar(ctx, "ABCDE", lr.ana, lobbyNow)
	require.NoError(t, err)
	assert.Equal(t, []byte("second"), avatar.Image)
	assert.True(t, second.Equal(avatar.UpdatedAt))
	assert.Equal(t, 1, count(t, lr.db, (*storage.PlayerAvatar)(nil)))

	t.Run("players carry only the version", func(t *testing.T) {
		players, err := lr.rooms.LoadPlayers(ctx, lr.roomID, matchID(t, lr))
		require.NoError(t, err)
		require.Len(t, players, 2)
		require.NotNil(t, players[0].AvatarAt)
		assert.True(t, second.Equal(*players[0].AvatarAt))
		assert.Nil(t, players[1].AvatarAt)
	})

	t.Run("only from its own, unexpired room", func(t *testing.T) {
		_, err := lr.rooms.LoadAvatar(ctx, "ZZZZZ", lr.ana, lobbyNow)
		require.ErrorIs(t, err, room.ErrAvatarNotFound)
		_, err = lr.rooms.LoadAvatar(ctx, "ABCDE", lr.ana, lobbyNow.Add(25*time.Hour))
		require.ErrorIs(t, err, room.ErrAvatarNotFound)
		_, err = lr.rooms.LoadAvatar(ctx, "ABCDE", lr.bia, lobbyNow)
		require.ErrorIs(t, err, room.ErrAvatarNotFound)
	})

	t.Run("the database bounds the size", func(t *testing.T) {
		err := tryInsert(lr.db, &storage.PlayerAvatar{PlayerID: lr.bia, Image: make([]byte, room.MaxAvatarBytes+1)})
		require.ErrorContains(t, err, "player_avatars_image_size_check")
		err = tryInsert(lr.db, &storage.PlayerAvatar{PlayerID: lr.bia, Image: []byte{}})
		require.ErrorContains(t, err, "player_avatars_image_size_check")
	})

	t.Run("deleting", func(t *testing.T) {
		require.NoError(t, lr.rooms.DeleteAvatar(ctx, lr.ana))
		require.NoError(t, lr.rooms.DeleteAvatar(ctx, lr.ana)) // nothing left: still fine
		_, err := lr.rooms.LoadAvatar(ctx, "ABCDE", lr.ana, lobbyNow)
		require.ErrorIs(t, err, room.ErrAvatarNotFound)
	})

	t.Run("goes away with the room", func(t *testing.T) {
		require.NoError(t, lr.rooms.SetAvatar(ctx, lr.bia, []byte("bia"), first))
		exec(t, lr.db.NewDelete().Model((*storage.Room)(nil)).Where("id = ?", lr.roomID))
		assert.Equal(t, 0, count(t, lr.db, (*storage.PlayerAvatar)(nil)))
	})
}
