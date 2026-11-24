package game

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidRounds(t *testing.T) {
	tests := []struct {
		rounds int
		want   bool
	}{
		{rounds: 3, want: true},
		{rounds: 5, want: true},
		{rounds: 8, want: true},
		{rounds: 10, want: true},
		{rounds: 12, want: true},
		{rounds: 0, want: false},
		{rounds: 4, want: false},
		{rounds: 13, want: false},
		{rounds: -5, want: false},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, ValidRounds(tt.rounds), "rounds=%d", tt.rounds)
	}
}
