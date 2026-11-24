package game

// PlayerID identifies a player inside a room.
type PlayerID string

// Vote is one player's vote. An empty Target is an abstention (no vote, or a player who dropped).
type Vote struct {
	Voter  PlayerID
	Target PlayerID
}

// Tally is the outcome of counting the votes of a round.
type Tally struct {
	// Counts holds the valid votes received by each player.
	Counts map[PlayerID]int
	// ImpostorFound is true only when the impostor received strictly more votes than anyone else.
	ImpostorFound bool
}

// CountVotes counts the votes of a round. Abstentions and self-votes are ignored, and any tie
// that involves the impostor lets the impostor escape. The caller must pass at most one vote
// per voter.
func CountVotes(votes []Vote, impostor PlayerID) Tally {
	counts := make(map[PlayerID]int)
	for _, v := range votes {
		if v.Target == "" || v.Target == v.Voter {
			continue
		}
		counts[v.Target]++
	}

	found := false
	if impostorVotes := counts[impostor]; impostorVotes > 0 {
		found = true
		for player, n := range counts {
			if player != impostor && n >= impostorVotes {
				found = false
				break
			}
		}
	}

	return Tally{Counts: counts, ImpostorFound: found}
}
