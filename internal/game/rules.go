// Package game holds the pure rules of the party game: phases, turn order, vote counting,
// scoring and rotation. It performs no I/O and does not depend on the database, HTTP or
// WebSocket layers, which keeps it trivially testable.
package game

import (
	"slices"
	"time"
)

// Limits and timings fixed by the specification (docs/specification.md).
const (
	MinPlayers = 3
	MaxPlayers = 20

	// DefaultTurnSeconds is the default time limit of a question turn.
	DefaultTurnSeconds = 180
	// TurnExtension is added to a turn each time the answerer or the host extends it.
	TurnExtension = time.Minute
	// VoteTimeout is how long the voting phase waits for missing votes.
	VoteTimeout = 5 * time.Minute
)

// Points awarded at the end of a round.
const (
	PointsCorrectVote         = 1
	PointsImpostorEscaped     = 2
	PointsImpostorGuessedWord = 1
)

// RoundOptions are the allowed numbers of rounds for a match.
var RoundOptions = []int{3, 5, 8, 10, 12}

// ValidRounds reports whether n is an allowed number of rounds.
func ValidRounds(n int) bool {
	return slices.Contains(RoundOptions, n)
}
