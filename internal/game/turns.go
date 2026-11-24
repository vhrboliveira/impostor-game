package game

import (
	"errors"
	"slices"
)

// Rand is the randomness the game needs. *rand.Rand from math/rand/v2 satisfies it, and tests
// inject a seeded generator to get deterministic results.
type Rand interface {
	IntN(n int) int
	Shuffle(n int, swap func(i, j int))
}

// TurnPair is one question turn: Asker reads a question aloud and Answerer replies.
type TurnPair struct {
	Asker    PlayerID
	Answerer PlayerID
}

// ErrNotEnoughPlayers is returned when a round cannot be built with fewer than MinPlayers.
var ErrNotEnoughPlayers = errors.New("not enough players")

// BuildTurnRing shuffles the players and chains them in a ring: each player asks the next one
// and the last asks the first. Everyone asks once and answers once, and whoever answers a turn
// is the one who asks in the following turn.
func BuildTurnRing(players []PlayerID, rng Rand) ([]TurnPair, error) {
	if len(players) < MinPlayers {
		return nil, ErrNotEnoughPlayers
	}

	order := slices.Clone(players)
	rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })

	pairs := make([]TurnPair, len(order))
	for i, asker := range order {
		pairs[i] = TurnPair{Asker: asker, Answerer: order[(i+1)%len(order)]}
	}
	return pairs, nil
}
