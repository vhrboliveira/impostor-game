package game

import (
	"errors"
	"slices"
)

// ErrNoCandidates is returned when nobody is eligible to be picked.
var ErrNoCandidates = errors.New("no eligible candidates")

// PickImpostor chooses the impostor of the next round. Every player of the round stays eligible
// in every draw: the results are public, so excluding earlier impostors would let the players
// deduce the impostor at the end of each cycle. Players picked less often in the match weigh
// more, which spreads the role over a match. history lists the impostors of the previous rounds
// of the match, oldest first.
func PickImpostor(players, history []PlayerID, rng Rand) (PlayerID, error) {
	return pickWeighted(players, history, nil, rng)
}

// PickSubject chooses the player used as the secret in a "players" theme. The impostor can never
// be the subject; every other player stays eligible, so the impostor cannot rule out earlier
// subjects, and players picked less often weigh more. history lists the subjects of the previous
// rounds of the match, oldest first.
func PickSubject(players, history []PlayerID, impostor PlayerID, rng Rand) (PlayerID, error) {
	return pickWeighted(players, history, []PlayerID{impostor}, rng)
}

// pickWeighted draws one of players, never one of excluded. Each candidate weighs
// 1 + (most picks of a current player) - (own picks): never zero, so no history removes a
// candidate, and the least picked are the most likely. Picks of players who left are ignored.
func pickWeighted(players, history, excluded []PlayerID, rng Rand) (PlayerID, error) {
	picks := make(map[PlayerID]int, len(players))
	for _, p := range history {
		if slices.Contains(players, p) {
			picks[p]++
		}
	}
	most := 0
	for _, p := range players {
		most = max(most, picks[p])
	}

	var pool []PlayerID
	for _, p := range players {
		if slices.Contains(excluded, p) {
			continue
		}
		for range 1 + most - picks[p] {
			pool = append(pool, p)
		}
	}
	if len(pool) == 0 {
		return "", ErrNoCandidates
	}
	return pool[rng.IntN(len(pool))], nil
}
