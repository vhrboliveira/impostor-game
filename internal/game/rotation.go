package game

import (
	"errors"
	"slices"
)

// ErrNoCandidates is returned when nobody is eligible to be picked.
var ErrNoCandidates = errors.New("no eligible candidates")

// PickImpostor chooses the impostor of the next round. Nobody repeats until everyone has been
// the impostor; after that the cycle starts over. history lists the impostors of the previous
// rounds of the match, oldest first.
func PickImpostor(players, history []PlayerID, rng Rand) (PlayerID, error) {
	return pickFromCycle(players, history, nil, rng)
}

// PickSubject chooses the player used as the secret in a "players" theme. The impostor can
// never be the subject, and the same player is only picked again after everyone has been.
// history lists the subjects of the previous rounds of the match, oldest first.
func PickSubject(players, history []PlayerID, impostor PlayerID, rng Rand) (PlayerID, error) {
	return pickFromCycle(players, history, []PlayerID{impostor}, rng)
}

func pickFromCycle(players, history, excluded []PlayerID, rng Rand) (PlayerID, error) {
	seen := cycleSeen(players, history)

	candidates := slices.DeleteFunc(slices.Clone(players), func(p PlayerID) bool {
		return seen[p] || slices.Contains(excluded, p)
	})
	if len(candidates) == 0 {
		// Only excluded players were left in the cycle: start a new one.
		candidates = slices.DeleteFunc(slices.Clone(players), func(p PlayerID) bool {
			return slices.Contains(excluded, p)
		})
	}
	if len(candidates) == 0 {
		return "", ErrNoCandidates
	}

	return candidates[rng.IntN(len(candidates))], nil
}

// cycleSeen replays the history and returns who has already been picked in the current cycle.
// The set is cleared every time all current players have been picked.
func cycleSeen(players, history []PlayerID) map[PlayerID]bool {
	seen := make(map[PlayerID]bool, len(players))
	for _, picked := range history {
		seen[picked] = true
		if allSeen(players, seen) {
			clear(seen)
		}
	}
	return seen
}

func allSeen(players []PlayerID, seen map[PlayerID]bool) bool {
	for _, p := range players {
		if !seen[p] {
			return false
		}
	}
	return true
}
