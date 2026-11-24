package game

// ScoreReason explains why points were awarded.
type ScoreReason string

// Score reasons.
const (
	ScoreCorrectVote         ScoreReason = "correct_vote"
	ScoreImpostorEscaped     ScoreReason = "impostor_escaped"
	ScoreImpostorGuessedWord ScoreReason = "impostor_guessed_word"
)

// ScoreEntry is a number of points awarded to a player for one reason.
type ScoreEntry struct {
	Player PlayerID
	Reason ScoreReason
	Points int
}

// ScoreRound returns the points earned in a round:
//   - +1 to each player who voted for the impostor (even if the impostor escaped);
//   - +2 to the impostor when not found;
//   - +1 to the impostor when found but guessing the secret.
//
// guessedSecret is only considered when the impostor was found.
func ScoreRound(votes []Vote, impostor PlayerID, impostorFound, guessedSecret bool) []ScoreEntry {
	var entries []ScoreEntry

	for _, v := range votes {
		if v.Target == impostor && v.Voter != impostor {
			entries = append(entries, ScoreEntry{Player: v.Voter, Reason: ScoreCorrectVote, Points: PointsCorrectVote})
		}
	}

	switch {
	case !impostorFound:
		entries = append(entries, ScoreEntry{Player: impostor, Reason: ScoreImpostorEscaped, Points: PointsImpostorEscaped})
	case guessedSecret:
		entries = append(entries, ScoreEntry{Player: impostor, Reason: ScoreImpostorGuessedWord, Points: PointsImpostorGuessedWord})
	}

	return entries
}
