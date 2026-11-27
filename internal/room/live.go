package room

import (
	"context"
	"time"

	"github.com/vhrboliveira/impostor-game/internal/game"
	"github.com/vhrboliveira/impostor-game/internal/protocol"
)

// RoomState is the state an actor loads from the database when a room becomes active.
type RoomState struct {
	RoomID    string
	Code      string
	ExpiresAt time.Time
	// EmptySince is non-nil while nobody is connected (see the retention rules).
	EmptySince *time.Time
	// Players are ordered by JoinedAt, oldest first.
	Players []PlayerState
	Match   MatchState
	// Round is the latest round of the match, nil before the first one.
	Round *RoundState
	// PastRounds are the earlier rounds of the match that had a theme, oldest first. They are
	// the history of the impostor and subject rotations.
	PastRounds []PastRound
	// Themes is the catalog offered in the theme selection phase, ordered by name.
	Themes []Theme
}

// Theme is a theme of the catalog.
type Theme struct {
	ID   int64
	Name string
	Kind game.ThemeKind
}

// PastRound is who was the impostor and, in a players theme, the subject of an earlier round.
type PastRound struct {
	ImpostorID string
	SubjectID  string
}

// PlayerState is a player of the room. Score is the sum of the current match.
type PlayerState struct {
	ID       string
	Name     string
	IsHost   bool
	JoinedAt time.Time
	Score    int
	// AvatarAt is when the player's photo was last set, nil without a photo.
	AvatarAt *time.Time
	// LeftAt is when the player left or was removed. They stay in the state so the names in the
	// round's turns and votes still resolve, but they hold no seat and never play again.
	LeftAt *time.Time
}

// Active reports whether the player is still in the room.
func (p PlayerState) Active() bool { return p.LeftAt == nil }

// MatchState is the latest match of the room.
type MatchState struct {
	ID          string
	Number      int
	Status      game.MatchStatus
	TotalRounds int
	TurnSeconds int
}

// RoundState is a round of the current match. Everything but ID, Number and Phase is set when
// the host picks the theme.
type RoundState struct {
	ID     string
	Number int
	Phase  game.Phase

	Theme      *Theme
	ThemeDrawn bool
	ImpostorID string
	// WordID is the secret of a words theme, SubjectID the player used as the secret of a
	// players theme.
	WordID    int64
	SubjectID string
	// Secret is what the other players see: the word, or the subject's name.
	Secret string
	// Turns is the whole ring in position order. The players of the round are its askers.
	Turns []TurnState
	// CurrentTurn is the position of the turn in progress during the questions phase.
	CurrentTurn int
	// PhaseDeadline is when the discussion, the voting or the guess ends.
	PhaseDeadline *time.Time

	// Votes maps each voter to their target, from the voting on. After the voting closes the
	// players who did not vote are in it too, with an empty target (abstention).
	Votes map[string]string
	// ImpostorFound is set when the voting closes.
	ImpostorFound bool
	// GuessOptions are offered to the found impostor in the guess phase.
	GuessOptions []GuessOption
	// Guess is the found impostor's guess, set when the round reaches the result.
	Guess *GuessState
	// Points are the points each player earned in the round, set in the result.
	Points map[string]int
}

// GuessOption is one possible secret offered to the found impostor. ID is the word id (in
// decimal) or the player id.
type GuessOption struct {
	ID    string
	Label string
}

// GuessState is the impostor's guess. OptionID is empty when time ran out.
type GuessState struct {
	OptionID string
	Correct  bool
}

// StartGuessParams closes the voting with the impostor found and opens the guess.
type StartGuessParams struct {
	RoundID string
	// Abstainers are the players of the round who did not vote.
	Abstainers []string
	OptionIDs  []string
	Deadline   time.Time
}

// FinishRoundParams closes a round: the outcome, the guess and the points.
type FinishRoundParams struct {
	RoundID string
	// Abstainers are recorded when the round goes straight from the voting to the result.
	Abstainers    []string
	ImpostorFound bool
	// The guess, when the impostor was found: the option picked (none when time ran out) and
	// whether it was the secret.
	GuessWordID   int64
	GuessPlayerID string
	GuessCorrect  bool
	Scores        []game.ScoreEntry
}

// PlayAgainParams opens a new match in the lobby of the same room.
type PlayAgainParams struct {
	RoomID      string
	Number      int
	TotalRounds int
	TurnSeconds int
	Now         time.Time
}

// TurnState is one turn of the ring. DeadlineAt is set once the turn starts.
type TurnState struct {
	ID         string
	Position   int
	AskerID    string
	AnswererID string
	Question   string
	DeadlineAt *time.Time
}

// Word is a candidate secret of a words theme.
type Word struct {
	ID   int64
	Text string
}

// Question is a candidate question for a round.
type Question struct {
	ID   int64
	Text string
}

// RoundCandidates is what a theme can offer to a new round in this room: its words never used
// in the room (empty for a players theme) and its questions (the theme's own plus the generic
// ones of its kind).
type RoundCandidates struct {
	Words     []Word
	Questions []Question
}

// PlannedTurn is one turn of a ring about to be written.
type PlannedTurn struct {
	AskerID    string
	AnswererID string
	QuestionID int64
}

// BeginRoundParams records the theme, the secret, the impostor and the turn ring of a round,
// moving it from the theme selection to the reveal.
type BeginRoundParams struct {
	RoundID    string
	ThemeID    int64
	ThemeDrawn bool
	// WordID is set for a words theme, SubjectID for a players theme.
	WordID     int64
	SubjectID  string
	ImpostorID string
	Turns      []PlannedTurn
}

// AdvanceTurnParams ends the turn in progress and starts the next one, or the discussion when
// it was the last turn (NextTurnID empty).
type AdvanceTurnParams struct {
	RoundID     string
	EndedTurnID string
	// EndedBy is "answerer", "host" or "timeout".
	EndedBy string
	Now     time.Time

	NextTurnID   string
	NextPosition int
	NextDeadline time.Time

	DiscussionDeadline time.Time
}

// StartMatchParams moves a match from the lobby to its first round.
type StartMatchParams struct {
	RoomID  string
	MatchID string
	Now     time.Time
}

// StartedMatch is the result of StartMatch. Players is re-read inside the same transaction, so
// it includes anyone who joined over REST just before the start.
type StartedMatch struct {
	Round   RoundState
	Players []PlayerState
}

// LiveStore is the persistence the room actors need. Implementations must be safe for
// concurrent use; each method is atomic.
type LiveStore interface {
	// Authenticate returns the id of the player of the room whose session token hashes to
	// tokenHash, and the id of that room. It returns ErrRoomNotFound when the room does not exist
	// or expired, and ErrUnauthorized when no player of the room has that token.
	Authenticate(ctx context.Context, code, tokenHash string, now time.Time) (playerID, roomID string, err error)

	// LoadRoom reads everything an actor needs. It returns ErrRoomNotFound when the room does
	// not exist or expired.
	LoadRoom(ctx context.Context, code string, now time.Time) (RoomState, error)

	// LoadPlayers re-reads the players of the room with their scores in the given match. Actors
	// call it when a player who joined over REST connects for the first time.
	LoadPlayers(ctx context.Context, roomID, matchID string) ([]PlayerState, error)

	// MarkConnected records the first time a player opened the room (later calls change
	// nothing): only such players, or players who joined a moment ago, hold a seat.
	MarkConnected(ctx context.Context, playerID string, at time.Time) error

	// SetEmptySince records that the room became empty (since is non-nil) or that someone is
	// connected again (since is nil). It returns ErrRoomNotFound when the room no longer exists.
	SetEmptySince(ctx context.Context, roomID string, since *time.Time) error

	// TransferHost makes playerID the only host of the room.
	TransferHost(ctx context.Context, roomID, playerID string) error

	// RemovePlayer marks the player as gone (left_at) and, when newHostID is not empty, makes that
	// player the host, in one transaction.
	RemovePlayer(ctx context.Context, roomID, playerID, newHostID string, at time.Time) error

	// StartMatch sets the match in progress and creates its first round in theme selection.
	StartMatch(ctx context.Context, params StartMatchParams) (StartedMatch, error)

	// LoadRoundCandidates returns the words and questions a theme can offer to a new round.
	LoadRoundCandidates(ctx context.Context, roomID string, theme Theme) (RoundCandidates, error)

	// BeginRound writes the plan of a round and moves it to the reveal. It returns the ids of the
	// turns, in position order.
	BeginRound(ctx context.Context, params BeginRoundParams) ([]string, error)

	// StartQuestions moves the round to the questions phase and starts its first turn.
	StartQuestions(ctx context.Context, roundID, turnID string, now, deadline time.Time) error

	// ExtendTurn moves the deadline of the turn in progress.
	ExtendTurn(ctx context.Context, turnID string, deadline time.Time) error

	// AdvanceTurn ends the turn in progress and starts the next one or the discussion.
	AdvanceTurn(ctx context.Context, params AdvanceTurnParams) error

	// ExtendDiscussion moves the end of the discussion.
	ExtendDiscussion(ctx context.Context, roundID string, deadline time.Time) error

	// StartVoting moves the round from the discussion to the voting, which ends at deadline.
	StartVoting(ctx context.Context, roundID string, deadline time.Time) error

	// CastVote records a vote. Votes are final.
	CastVote(ctx context.Context, roundID, voterID, targetID string, now time.Time) error

	// LoadGuessWords returns the words of a theme that no round of the room other than roundID
	// used: the set the secret was drawn from, for the impostor's guess options.
	LoadGuessWords(ctx context.Context, roomID string, themeID int64, roundID string) ([]Word, error)

	// StartGuess closes the voting with the impostor found and moves to the guess.
	StartGuess(ctx context.Context, params StartGuessParams) error

	// FinishRound records the outcome and the points and moves the round to the result.
	FinishRound(ctx context.Context, params FinishRoundParams) error

	// NextRound creates round number of the match, in theme selection, and returns its id.
	NextRound(ctx context.Context, matchID string, number int, now time.Time) (string, error)

	// SetAvatar stores the player's photo (an already normalized JPEG), replacing any previous
	// one, with at as its new version.
	SetAvatar(ctx context.Context, playerID string, image []byte, at time.Time) error

	// DeleteAvatar removes the player's photo, if any.
	DeleteAvatar(ctx context.Context, playerID string) error

	// LoadAvatar returns the photo of a player of the room. It returns ErrAvatarNotFound when
	// the room does not exist or expired, the player is not in it or has no photo.
	LoadAvatar(ctx context.Context, code, playerID string, now time.Time) (Avatar, error)

	// EndMatch closes a match in progress as finished or ended early.
	EndMatch(ctx context.Context, matchID string, status game.MatchStatus, now time.Time) error

	// PlayAgain opens a new match in the lobby of the room and returns it.
	PlayAgain(ctx context.Context, params PlayAgainParams) (MatchState, error)
}

// Client is one WebSocket connection of a player, as seen by the room. Implementations must
// never block: the actor calls them while processing the room's commands.
type Client interface {
	// Send queues a message. A connection that cannot keep up closes itself; the player gets a
	// full snapshot when it reconnects.
	Send(msg protocol.ServerMessage)
	// Close ends the connection with a WebSocket close code (for example
	// protocol.CloseRoomNotFound when the room expires).
	Close(code int, reason string)
}

// Clock abstracts time so tests can drive the actors' timers.
type Clock interface {
	Now() time.Time
	// AfterFunc calls f in its own goroutine after d. stop cancels the call and reports whether
	// it did so before f started.
	AfterFunc(d time.Duration, f func()) (stop func() bool)
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) AfterFunc(d time.Duration, f func()) func() bool {
	return time.AfterFunc(d, f).Stop
}
