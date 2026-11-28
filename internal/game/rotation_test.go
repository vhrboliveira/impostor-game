package game

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// counts picks over many seeds.
func counts(t *testing.T, pick func(Rand) (PlayerID, error)) map[PlayerID]int {
	t.Helper()
	out := map[PlayerID]int{}
	for seed := range uint64(300) {
		p, err := pick(newRand(seed))
		require.NoError(t, err)
		out[p]++
	}
	return out
}

// Every player stays eligible in every draw: with public results, excluding the earlier
// impostors would let the players deduce who the impostor is at the end of each cycle.
func TestPickImpostor_EveryPlayerStaysEligible(t *testing.T) {
	got := counts(t, func(r Rand) (PlayerID, error) {
		return PickImpostor([]PlayerID{"a", "b", "c"}, []PlayerID{"a", "b"}, r)
	})

	assert.Positive(t, got["a"], "an earlier impostor can be drawn again")
	assert.Positive(t, got["b"], "an earlier impostor can be drawn again")
	assert.Greater(t, got["c"], got["a"], "who was picked less is more likely")
	assert.Greater(t, got["c"], got["b"], "who was picked less is more likely")
}

func TestPickImpostor_SpreadsThePicksOverAMatch(t *testing.T) {
	players := []PlayerID{"a", "b", "c"}
	rng := newRand(5)
	var history []PlayerID
	for range 300 {
		picked, err := PickImpostor(players, history, rng)
		require.NoError(t, err)
		history = append(history, picked)
	}
	for _, p := range players {
		n := 0
		for _, h := range history {
			if h == p {
				n++
			}
		}
		assert.InDelta(t, 100, n, 15, "player %s", p)
	}
}

func TestPickImpostor_IgnoresHistoryOfPlayersWhoLeft(t *testing.T) {
	// "gone" left the room: only the current players' picks weigh.
	got := counts(t, func(r Rand) (PlayerID, error) {
		return PickImpostor([]PlayerID{"a", "b", "c"}, []PlayerID{"gone", "a", "b"}, r)
	})

	assert.NotContains(t, got, PlayerID("gone"))
	assert.Greater(t, got["c"], got["a"])
}

func TestPickSubject_NeverPicksTheImpostorButKeepsEarlierSubjects(t *testing.T) {
	got := counts(t, func(r Rand) (PlayerID, error) {
		return PickSubject([]PlayerID{"a", "b", "c"}, []PlayerID{"a"}, "b", r)
	})

	assert.NotContains(t, got, PlayerID("b"), "the impostor is never the subject")
	assert.Positive(t, got["a"], "an earlier subject stays possible, so the impostor cannot rule it out")
	assert.Greater(t, got["c"], got["a"])
}

func TestPickSubject_FailsWhenNobodyIsEligible(t *testing.T) {
	_, err := PickSubject([]PlayerID{"a"}, nil, "a", newRand(1))

	require.ErrorIs(t, err, ErrNoCandidates)
}
