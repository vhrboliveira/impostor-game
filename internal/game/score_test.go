package game

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestScoreRound(t *testing.T) {
	const impostor PlayerID = "imp"

	votesFoundByTwo := []Vote{
		{Voter: "a", Target: "imp"},
		{Voter: "b", Target: "imp"},
		{Voter: "c", Target: "a"},
	}

	tests := []struct {
		name          string
		votes         []Vote
		found         bool
		guessedSecret bool
		want          []ScoreEntry
	}{
		{
			name:  "impostor found without guessing: only correct voters score",
			votes: votesFoundByTwo,
			found: true,
			want: []ScoreEntry{
				{Player: "a", Reason: ScoreCorrectVote, Points: 1},
				{Player: "b", Reason: ScoreCorrectVote, Points: 1},
			},
		},
		{
			name:          "impostor found but guesses the secret: bonus point",
			votes:         votesFoundByTwo,
			found:         true,
			guessedSecret: true,
			want: []ScoreEntry{
				{Player: "a", Reason: ScoreCorrectVote, Points: 1},
				{Player: "b", Reason: ScoreCorrectVote, Points: 1},
				{Player: impostor, Reason: ScoreImpostorGuessedWord, Points: 1},
			},
		},
		{
			name: "impostor escapes: two points, correct voters still score",
			votes: []Vote{
				{Voter: "a", Target: "imp"},
				{Voter: "b", Target: "c"},
			},
			found: false,
			want: []ScoreEntry{
				{Player: "a", Reason: ScoreCorrectVote, Points: 1},
				{Player: impostor, Reason: ScoreImpostorEscaped, Points: 2},
			},
		},
		{
			name:          "a guess is ignored when the impostor was not found",
			votes:         nil,
			found:         false,
			guessedSecret: true,
			want: []ScoreEntry{
				{Player: impostor, Reason: ScoreImpostorEscaped, Points: 2},
			},
		},
		{
			name: "the impostor voting for someone else earns nothing",
			votes: []Vote{
				{Voter: "imp", Target: "a"},
				{Voter: "imp2", Target: "imp"},
			},
			found: true,
			want: []ScoreEntry{
				{Player: "imp2", Reason: ScoreCorrectVote, Points: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScoreRound(tt.votes, impostor, tt.found, tt.guessedSecret)

			assert.Equal(t, tt.want, got)
		})
	}
}
