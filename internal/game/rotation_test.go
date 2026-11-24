package game

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPickImpostor_NobodyRepeatsWithinACycle(t *testing.T) {
	players := []PlayerID{"a", "b", "c", "d"}
	rng := newRand(5)

	var history []PlayerID
	for range 8 {
		picked, err := PickImpostor(players, history, rng)
		require.NoError(t, err)
		history = append(history, picked)
	}

	assert.ElementsMatch(t, players, history[:4], "first cycle")
	assert.ElementsMatch(t, players, history[4:], "second cycle")
}

func TestPickImpostor_IncludesPlayersWhoJoinedLater(t *testing.T) {
	// "a" and "b" already played; "c" joined between rounds and must be the only candidate.
	picked, err := PickImpostor([]PlayerID{"a", "b", "c"}, []PlayerID{"a", "b"}, newRand(1))

	require.NoError(t, err)
	assert.Equal(t, PlayerID("c"), picked)
}

func TestPickImpostor_IgnoresHistoryOfPlayersWhoLeft(t *testing.T) {
	// "gone" left the room: it must not stop the cycle from completing.
	players := []PlayerID{"a", "b", "c"}
	history := []PlayerID{"gone", "a", "b"}

	picked, err := PickImpostor(players, history, newRand(1))

	require.NoError(t, err)
	assert.Equal(t, PlayerID("c"), picked)
}

func TestPickSubject_NeverPicksTheImpostorAndPrefersUnseenPlayers(t *testing.T) {
	players := []PlayerID{"a", "b", "c"}

	for seed := range uint64(20) {
		picked, err := PickSubject(players, []PlayerID{"a"}, "b", newRand(seed))

		require.NoError(t, err)
		assert.Equal(t, PlayerID("c"), picked, "seed=%d", seed)
	}
}

func TestPickSubject_RestartsTheCycleWhenOnlyTheImpostorIsLeft(t *testing.T) {
	players := []PlayerID{"a", "b", "c"}

	picked, err := PickSubject(players, []PlayerID{"a", "c"}, "b", newRand(9))

	require.NoError(t, err)
	assert.NotEqual(t, PlayerID("b"), picked)
}

func TestPickSubject_FailsWhenNobodyIsEligible(t *testing.T) {
	_, err := PickSubject([]PlayerID{"a"}, nil, "a", newRand(1))

	require.ErrorIs(t, err, ErrNoCandidates)
}
