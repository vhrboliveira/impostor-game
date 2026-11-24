package game

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCountVotes(t *testing.T) {
	const impostor PlayerID = "imp"

	tests := []struct {
		name          string
		votes         []Vote
		wantFound     bool
		wantImpostors int
	}{
		{
			name: "strict plurality finds the impostor",
			votes: []Vote{
				{Voter: "a", Target: "imp"},
				{Voter: "b", Target: "imp"},
				{Voter: "c", Target: "a"},
				{Voter: "imp", Target: "b"},
			},
			wantFound:     true,
			wantImpostors: 2,
		},
		{
			name: "a tie with another player lets the impostor escape",
			votes: []Vote{
				{Voter: "a", Target: "imp"},
				{Voter: "b", Target: "c"},
			},
			wantFound:     false,
			wantImpostors: 1,
		},
		{
			name: "fewer votes than another player lets the impostor escape",
			votes: []Vote{
				{Voter: "a", Target: "imp"},
				{Voter: "b", Target: "c"},
				{Voter: "d", Target: "c"},
			},
			wantFound:     false,
			wantImpostors: 1,
		},
		{
			name:          "no votes at all",
			votes:         nil,
			wantFound:     false,
			wantImpostors: 0,
		},
		{
			name: "abstentions are ignored",
			votes: []Vote{
				{Voter: "a", Target: ""},
				{Voter: "b", Target: ""},
			},
			wantFound:     false,
			wantImpostors: 0,
		},
		{
			name: "a single vote is enough when nobody else gets one",
			votes: []Vote{
				{Voter: "a", Target: "imp"},
				{Voter: "b", Target: ""},
			},
			wantFound:     true,
			wantImpostors: 1,
		},
		{
			name: "self votes are ignored",
			votes: []Vote{
				{Voter: "imp", Target: "imp"},
			},
			wantFound:     false,
			wantImpostors: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tally := CountVotes(tt.votes, impostor)

			assert.Equal(t, tt.wantFound, tally.ImpostorFound)
			assert.Equal(t, tt.wantImpostors, tally.Counts[impostor])
		})
	}
}
