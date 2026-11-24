package game

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPhaseNext_FollowsTheRoundOrder(t *testing.T) {
	want := []Phase{PhaseReveal, PhaseQuestions, PhaseDiscussion, PhaseVoting, PhaseResult}

	current := PhaseThemeSelection
	for _, expected := range want {
		next, ok := current.Next()
		require.True(t, ok)
		assert.Equal(t, expected, next)
		current = next
	}
}

func TestPhaseNext_StopsAfterResult(t *testing.T) {
	next, ok := PhaseResult.Next()

	assert.False(t, ok)
	assert.Equal(t, Phase(""), next)
}

func TestPhaseNext_RejectsUnknownPhase(t *testing.T) {
	_, ok := Phase("nope").Next()

	assert.False(t, ok)
}

func TestPhaseValid(t *testing.T) {
	assert.True(t, PhaseVoting.Valid())
	assert.False(t, Phase("").Valid())
	assert.False(t, Phase("nope").Valid())
}

func TestThemeKindValid(t *testing.T) {
	assert.True(t, ThemeKindWords.Valid())
	assert.True(t, ThemeKindPlayers.Valid())
	assert.False(t, ThemeKind("other").Valid())
}
