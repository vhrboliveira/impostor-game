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
	// DiscussionDuration is how long the discussion lasts before the voting starts. The host can
	// end it earlier.
	DiscussionDuration = 3 * time.Minute
	// DiscussionExtension is added to the discussion each time the host extends it.
	DiscussionExtension = time.Minute
	// VoteTimeout is how long the voting phase waits for missing votes.
	VoteTimeout = 5 * time.Minute
	// GuessTimeout is how long a found impostor has to guess the secret. No answer counts as a
	// wrong guess.
	GuessTimeout = 60 * time.Second
	// GuessOptions is how many options the impostor picks from (the secret among them). Fewer
	// when the theme has fewer words or the round fewer players.
	GuessOptions = 8
	// MinGuessWords is the fewest unused words a words theme needs to feed a round. The guess
	// options come from the same unused words, so with fewer the found impostor's guess would
	// narrow down to the secret.
	MinGuessWords = 4
	// SeatHoldTime is how long a player who joined but never opened the room (no WebSocket yet)
	// holds a seat against MaxPlayers.
	SeatHoldTime = 2 * time.Minute
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
