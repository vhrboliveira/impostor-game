package room

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vhrboliveira/impostor-game/internal/protocol"
)

func leave() protocol.ClientMessage { return protocol.ClientMessage{Type: protocol.TypeLeaveRoom} }

func removeMsg(id string) protocol.ClientMessage {
	return protocol.ClientMessage{Type: protocol.TypeRemovePlayer, Remove: &protocol.RemovePlayerPayload{PlayerID: id}}
}

func TestLeaveRoom_ThePlayerIsMarkedAndDisconnected(t *testing.T) {
	h := newHarness(t)
	_, ana := h.connect("t-ana")
	bia, biaClient := h.connect("t-bia")

	bia.Handle(leave())
	h.settle()

	assert.Equal(t, []string{"bia"}, h.store.removed)
	assert.True(t, biaClient.closed)
	assert.Equal(t, protocol.CloseRemoved, biaClient.closeCode)
	gone := playerByID(t, ana.lastSnapshot(t), "bia")
	assert.True(t, gone.HasLeft, "listed, so the names of the round still resolve")
	assert.False(t, gone.IsConnected)

	// The same token no longer opens the room.
	_, err := h.m.Connect(context.Background(), "K7M2P", "t-bia", &fakeClient{})
	require.ErrorIs(t, err, ErrUnauthorized)
}

func TestLeaveRoom_TheHostHandsTheRoleOnAtOnce(t *testing.T) {
	h := newHarness(t)
	ana, _ := h.connect("t-ana")
	_, bia := h.connect("t-bia")

	ana.Handle(leave())
	h.settle()

	s := bia.lastSnapshot(t)
	assert.True(t, playerByID(t, s, "bia").IsHost, "the oldest connected player, without the grace period")
	assert.False(t, playerByID(t, s, "ana").IsHost)
}

func TestRemovePlayer_OnlyTheHostRemovesSomeoneElse(t *testing.T) {
	h := newHarness(t)
	ana, anaClient := h.connect("t-ana")
	bia, biaClient := h.connect("t-bia")
	_, caioClient := h.connect("t-caio")

	bia.Handle(removeMsg("caio"))
	h.settle()
	assert.Equal(t, protocol.ErrCodeNotHost, biaClient.lastError())

	for _, id := range []string{"ana", "stranger"} {
		ana.Handle(removeMsg(id))
		h.settle()
		assert.Equal(t, protocol.ErrCodeInvalidPlayer, anaClient.lastError(), id)
	}
	assert.Empty(t, h.store.removed)

	ana.Handle(removeMsg("caio"))
	h.settle()
	assert.Equal(t, []string{"caio"}, h.store.removed)
	assert.Equal(t, protocol.CloseRemoved, caioClient.closeCode)
	assert.True(t, playerByID(t, anaClient.lastSnapshot(t), "caio").HasLeft)

	ana.Handle(removeMsg("caio"))
	h.settle()
	assert.Equal(t, protocol.ErrCodeInvalidPlayer, anaClient.lastError(), "already gone")
}

func TestOfflineRemoval_AfterThreeMinutesAway(t *testing.T) {
	h := newHarness(t)
	_, ana := h.connect("t-ana")
	bia, _ := h.connect("t-bia")
	_, _ = h.connect("t-caio")

	bia.Leave() // the socket dropped
	h.settle()
	h.clock.Advance(DefaultOfflineRemoval - time.Second)
	h.settle()
	assert.Empty(t, h.store.removed)

	h.clock.Advance(time.Second)
	h.settle()
	assert.Equal(t, []string{"bia"}, h.store.removed)
	assert.True(t, playerByID(t, ana.lastSnapshot(t), "bia").HasLeft)
}

func TestOfflineRemoval_CancelledWhenThePlayerReturns(t *testing.T) {
	h := newHarness(t)
	_, _ = h.connect("t-ana")
	bia, _ := h.connect("t-bia")

	bia.Leave()
	h.settle()
	h.clock.Advance(2 * time.Minute)
	h.settle()
	_, _ = h.connect("t-bia")
	h.clock.Advance(2 * time.Minute)
	h.settle()

	assert.NotContains(t, h.store.removed, "bia")
}

func TestOfflineRemoval_PlayersAwayWhenTheRoomLoads(t *testing.T) {
	h := newHarness(t)
	_, ana := h.connect("t-ana") // bia and caio never came back after a restart

	h.clock.Advance(DefaultOfflineRemoval)
	h.settle()

	assert.ElementsMatch(t, []string{"bia", "caio"}, h.store.removed)
	s := ana.lastSnapshot(t)
	assert.True(t, playerByID(t, s, "bia").HasLeft)
	assert.False(t, playerByID(t, s, "ana").HasLeft)
}
