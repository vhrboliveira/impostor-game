package protocol

import "time"

// Message types sent by the server.
const (
	TypeRoomSnapshot = "room_snapshot"
	TypeError        = "error"
	TypePong         = "pong"
)

// Error codes carried by error messages. The web app translates them.
const (
	ErrCodeInvalidMessage   = "invalid_message"
	ErrCodeNotHost          = "not_host"
	ErrCodeNotEnoughPlayers = "not_enough_players"
	ErrCodeInvalidPhase     = "invalid_phase"
	// ErrCodeNotYourTurn: only the current answerer (or the host) can advance or extend a turn.
	ErrCodeNotYourTurn = "not_your_turn"
	// ErrCodeNotParticipant: the player is not in the current round (they were offline when it
	// started).
	ErrCodeNotParticipant = "not_participant"
	// ErrCodeThemeUnavailable: the theme has no unused word or not enough questions for the
	// players in the round.
	ErrCodeThemeUnavailable = "theme_unavailable"
	// ErrCodeInvalidVote: a vote for oneself or for someone outside the round.
	ErrCodeInvalidVote = "invalid_vote"
	// ErrCodeNotImpostor: only the found impostor guesses.
	ErrCodeNotImpostor = "not_impostor"
	// ErrCodeInvalidGuess: the option is not one of the guess options.
	ErrCodeInvalidGuess = "invalid_guess"
	// ErrCodeInvalidRounds: play_again with a number of rounds that is not 3, 5, 8, 10 or 12.
	ErrCodeInvalidRounds = "invalid_rounds"
	// ErrCodeInvalidPlayer: remove_player names nobody of the room, or the host themselves.
	ErrCodeInvalidPlayer = "invalid_player"
	ErrCodeInternal      = "internal_error"
)

// Application close codes. The client does not reconnect after them.
const (
	CloseUnauthorized = 4401
	// CloseRemoved: the player left the room or was removed (by the host, or offline too long).
	CloseRemoved      = 4403
	CloseRoomNotFound = 4404
)

// Standard close codes the server also uses. The client reconnects after them.
const (
	// ClosePolicyViolation (1008) closes a connection that sent messages too fast, or the oldest
	// connection of a player who opened too many.
	ClosePolicyViolation = 1008
	// CloseInternalError (1011) closes the connections of a room whose actor failed; reconnecting
	// loads the room again.
	CloseInternalError = 1011
)

// Roles of a player in a round, as seen by that player.
const (
	RoleInsider  = "insider"
	RoleImpostor = "impostor"
)

// ServerMessage is a message from the server to one connection.
type ServerMessage struct {
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
}

// ErrorPayload is the payload of an error message.
type ErrorPayload struct {
	Code string `json:"code"`
}

// SnapshotMessage wraps the state of the room as seen by one player.
func SnapshotMessage(snapshot RoomSnapshot) ServerMessage {
	return ServerMessage{Type: TypeRoomSnapshot, Payload: snapshot}
}

// ErrorMessage reports a rejected command. The connection stays open.
func ErrorMessage(code string) ServerMessage {
	return ServerMessage{Type: TypeError, Payload: ErrorPayload{Code: code}}
}

// PongMessage answers a ping.
func PongMessage() ServerMessage {
	return ServerMessage{Type: TypePong}
}

// RoomSnapshot is the full state of a room as one player may see it. It is sent on every
// connection and after every change.
//
// Security rules: Me.Secret is nil for the impostor, Turns stops at the current turn and Result
// is only set in the result phase.
type RoomSnapshot struct {
	RoomCode string       `json:"roomCode"`
	Match    MatchView    `json:"match"`
	Players  []PlayerView `json:"players"`
	Round    *RoundView   `json:"round"`
	Me       MeView       `json:"me"`
}

// MatchView summarizes the current match. RoundNumber is 0 before the first round.
type MatchView struct {
	Status      string `json:"status"`
	TotalRounds int    `json:"totalRounds"`
	RoundNumber int    `json:"roundNumber"`
}

// PlayerView is a player as everyone in the room sees it. Score is the sum of the current match.
type PlayerView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	IsHost      bool   `json:"isHost"`
	IsConnected bool   `json:"isConnected"`
	Score       int    `json:"score"`
	// AvatarURL is the player's photo (GET, versioned so it can be cached), null without one.
	AvatarURL *string `json:"avatarUrl"`
	// HasLeft marks a player who left the room. They are still listed so the names of the round
	// resolve; the app hides them from the lobby, the scoreboard and the podium.
	HasLeft bool `json:"hasLeft"`
}

// RoundView is the current round. Slices are never nil, so they encode as [] and not null.
type RoundView struct {
	Number int    `json:"number"`
	Phase  string `json:"phase"`
	// ThemeOptions is the catalog, only in the theme selection phase.
	ThemeOptions []ThemeOption `json:"themeOptions"`
	// ThemeName is the chosen theme, from the reveal on.
	ThemeName *string `json:"themeName"`
	// TotalTurns is the size of the turn ring (one turn per player in the round), 0 before the
	// theme is chosen. The last turn is position TotalTurns-1.
	TotalTurns int `json:"totalTurns"`
	// ConfirmedIDs are the players who tapped "Entendi" in the reveal phase.
	ConfirmedIDs []string `json:"confirmedIds"`
	// PhaseDeadlineAt is when the discussion, the voting or the guess ends; null in the other
	// phases.
	PhaseDeadlineAt *time.Time `json:"phaseDeadlineAt"`
	// VotedIDs are the players who already voted, during the voting.
	VotedIDs []string `json:"votedIds"`
	// GuessOptions are the options of the impostor's guess, during the guess.
	GuessOptions []GuessOption `json:"guessOptions"`
	Turns        []TurnView    `json:"turns"`
	Result       *RoundResult  `json:"result"`
}

// GuessOption is one possible secret offered to the found impostor: a word, or a player in a
// players theme.
type GuessOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// ThemeOption is a theme the host can pick in the theme selection phase.
type ThemeOption struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// TurnView is one turn of the ring: who asks whom and the question. DeadlineAt is only set on the
// turn in progress.
type TurnView struct {
	Position   int        `json:"position"`
	AskerID    string     `json:"askerId"`
	AnswererID string     `json:"answererId"`
	Question   string     `json:"question"`
	DeadlineAt *time.Time `json:"deadlineAt"`
}

// RoundResult is revealed to everyone in the result phase.
type RoundResult struct {
	ImpostorID    string `json:"impostorId"`
	ImpostorFound bool   `json:"impostorFound"`
	Secret        string `json:"secret"`
	// Votes lists every player of the round in ring order; TargetID is null for an abstention.
	Votes []VoteView `json:"votes"`
	// Guess is the found impostor's guess; null when the impostor escaped.
	Guess *GuessView `json:"guess"`
	// Points are the points each player earned in this round (players with none are left out).
	Points []PointsView `json:"points"`
}

// VoteView is who voted for whom.
type VoteView struct {
	VoterID  string  `json:"voterId"`
	TargetID *string `json:"targetId"`
}

// GuessView is the impostor's guess. Label is null when time ran out without a pick.
type GuessView struct {
	Label   *string `json:"label"`
	Correct bool    `json:"correct"`
}

// PointsView is the points a player earned in the round.
type PointsView struct {
	PlayerID string `json:"playerId"`
	Points   int    `json:"points"`
}

// MeView is what only the receiving player sees.
type MeView struct {
	PlayerID     string  `json:"playerId"`
	Role         *string `json:"role"`
	Secret       *string `json:"secret"`
	VoteTargetID *string `json:"voteTargetId"`
}
