package game

import "slices"

// Phase is a step of a round. The server drives the transitions; clients only render them.
type Phase string

// Phases of a round, in order.
const (
	PhaseThemeSelection Phase = "theme_selection"
	PhaseReveal         Phase = "reveal"
	PhaseQuestions      Phase = "questions"
	PhaseDiscussion     Phase = "discussion"
	PhaseVoting         Phase = "voting"
	// PhaseGuess only happens when the impostor was found: they try to guess the secret.
	PhaseGuess  Phase = "guess"
	PhaseResult Phase = "result"
)

var phaseOrder = []Phase{
	PhaseThemeSelection,
	PhaseReveal,
	PhaseQuestions,
	PhaseDiscussion,
	PhaseVoting,
	PhaseGuess,
	PhaseResult,
}

// Valid reports whether p is a known phase.
func (p Phase) Valid() bool {
	return slices.Contains(phaseOrder, p)
}

// Next returns the phase that follows p. ok is false for the last phase and for unknown phases.
func (p Phase) Next() (next Phase, ok bool) {
	i := slices.Index(phaseOrder, p)
	if i < 0 || i == len(phaseOrder)-1 {
		return "", false
	}
	return phaseOrder[i+1], true
}

// MatchStatus is the lifecycle state of a match.
type MatchStatus string

// Match statuses.
const (
	MatchLobby      MatchStatus = "lobby"
	MatchInProgress MatchStatus = "in_progress"
	MatchFinished   MatchStatus = "finished"
	MatchEndedEarly MatchStatus = "ended_early"
)

// ThemeKind tells what a theme draws its secret from.
type ThemeKind string

// Theme kinds.
const (
	// ThemeKindWords themes use a word from the catalog.
	ThemeKindWords ThemeKind = "words"
	// ThemeKindPlayers themes use one of the players in the room.
	ThemeKindPlayers ThemeKind = "players"
)

// Valid reports whether k is a known theme kind.
func (k ThemeKind) Valid() bool {
	return k == ThemeKindWords || k == ThemeKindPlayers
}
