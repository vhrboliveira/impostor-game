package game

import (
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRand(seed uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, seed+1))
}

func TestBuildTurnRing_EveryoneAsksOnceAndAnswersOnce(t *testing.T) {
	players := []PlayerID{"a", "b", "c", "d", "e"}

	pairs, err := BuildTurnRing(players, newRand(1))

	require.NoError(t, err)
	require.Len(t, pairs, len(players))

	var askers, answerers []PlayerID
	for _, p := range pairs {
		assert.NotEqual(t, p.Asker, p.Answerer)
		askers = append(askers, p.Asker)
		answerers = append(answerers, p.Answerer)
	}
	assert.ElementsMatch(t, players, askers)
	assert.ElementsMatch(t, players, answerers)
}

func TestBuildTurnRing_AnswererAsksNextAndRingCloses(t *testing.T) {
	pairs, err := BuildTurnRing([]PlayerID{"a", "b", "c", "d"}, newRand(7))

	require.NoError(t, err)
	for i := range pairs {
		next := pairs[(i+1)%len(pairs)]
		assert.Equal(t, pairs[i].Answerer, next.Asker)
	}
}

func TestBuildTurnRing_IsDeterministicForTheSameSeed(t *testing.T) {
	players := []PlayerID{"a", "b", "c", "d", "e", "f"}

	first, err := BuildTurnRing(players, newRand(42))
	require.NoError(t, err)
	second, err := BuildTurnRing(players, newRand(42))
	require.NoError(t, err)

	assert.Equal(t, first, second)
}

func TestBuildTurnRing_DoesNotMutateTheInput(t *testing.T) {
	players := []PlayerID{"a", "b", "c", "d"}

	_, err := BuildTurnRing(players, newRand(3))

	require.NoError(t, err)
	assert.Equal(t, []PlayerID{"a", "b", "c", "d"}, players)
}

func TestBuildTurnRing_RequiresMinimumPlayers(t *testing.T) {
	_, err := BuildTurnRing([]PlayerID{"a", "b"}, newRand(1))

	require.ErrorIs(t, err, ErrNotEnoughPlayers)
}
