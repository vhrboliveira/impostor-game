package storage

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

// The models below map the tables to Go structs for the bun query builder. They do not define
// the schema: the SQL migrations do, with the constraints that struct tags cannot express.
//
// Columns whose database default differs from the Go zero value (generated ids, timestamps,
// 'lobby', 'theme_selection', 180, 'words') are tagged `nullzero`, so a zero value is written as
// DEFAULT. Nullable columns are pointers. Every model lists every column of its table: reading a
// whole row (Returning("*"), SELECT *) fails on a column the model lacks, so a migration that
// adds a column adds the field here in the same change.

// Theme is a row of the themes table.
type Theme struct {
	bun.BaseModel `bun:"table:themes,alias:t"`

	ID   int64  `bun:"id,pk,autoincrement"`
	Slug string `bun:"slug,notnull"`
	Name string `bun:"name,notnull"`
	Kind string `bun:"kind,notnull"`
}

// Word is a row of the words table. Kind is always 'words' (the database default): together with
// theme_id it references themes (id, kind), so a word can only belong to a words theme.
type Word struct {
	bun.BaseModel `bun:"table:words,alias:w"`

	ID      int64  `bun:"id,pk,autoincrement"`
	ThemeID int64  `bun:"theme_id,notnull"`
	Kind    string `bun:"kind,nullzero,notnull"`
	Text    string `bun:"text,notnull"`
}

// Question is a row of the questions table. A nil ThemeID is a generic question.
type Question struct {
	bun.BaseModel `bun:"table:questions,alias:q"`

	ID      int64  `bun:"id,pk,autoincrement"`
	Kind    string `bun:"kind,notnull"`
	ThemeID *int64 `bun:"theme_id"`
	Text    string `bun:"text,notnull"`
}

// Room is a row of the rooms table.
type Room struct {
	bun.BaseModel `bun:"table:rooms,alias:r"`

	ID         string     `bun:"id,pk,type:uuid,nullzero"`
	Code       string     `bun:"code,notnull"`
	CreatedAt  time.Time  `bun:"created_at,nullzero,notnull"`
	ExpiresAt  time.Time  `bun:"expires_at,notnull"`
	EmptySince *time.Time `bun:"empty_since"`
}

// Player is a row of the players table.
type Player struct {
	bun.BaseModel `bun:"table:players,alias:p"`

	ID         string    `bun:"id,pk,type:uuid,nullzero"`
	RoomID     string    `bun:"room_id,type:uuid,notnull"`
	Name       string    `bun:"name,notnull"`
	IsHost     bool      `bun:"is_host,notnull"`
	TokenHash  string    `bun:"token_hash,notnull"`
	JoinedAt   time.Time `bun:"joined_at,nullzero,notnull"`
	LastSeenAt time.Time `bun:"last_seen_at,nullzero,notnull"`
}

// PlayerAvatar is a row of the player_avatars table: a player's photo, as a JPEG.
type PlayerAvatar struct {
	bun.BaseModel `bun:"table:player_avatars,alias:pa"`

	PlayerID  string    `bun:"player_id,pk,type:uuid"`
	Image     []byte    `bun:"image,notnull"`
	UpdatedAt time.Time `bun:"updated_at,nullzero,notnull"`
}

// Match is a row of the matches table.
type Match struct {
	bun.BaseModel `bun:"table:matches,alias:m"`

	ID          string     `bun:"id,pk,type:uuid,nullzero"`
	RoomID      string     `bun:"room_id,type:uuid,notnull"`
	Number      int        `bun:"number,notnull"`
	Status      string     `bun:"status,nullzero,notnull"`
	TotalRounds int        `bun:"total_rounds,notnull"`
	TurnSeconds int        `bun:"turn_seconds,nullzero,notnull"`
	CreatedAt   time.Time  `bun:"created_at,nullzero,notnull"`
	StartedAt   *time.Time `bun:"started_at"`
	EndedAt     *time.Time `bun:"ended_at"`
}

// Round is a row of the rounds table. Word and Subject are loaded with Relation.
type Round struct {
	bun.BaseModel `bun:"table:rounds,alias:rd"`

	ID               string          `bun:"id,pk,type:uuid,nullzero"`
	MatchID          string          `bun:"match_id,type:uuid,notnull"`
	Number           int             `bun:"number,notnull"`
	Phase            string          `bun:"phase,nullzero,notnull"`
	PhaseDeadlineAt  *time.Time      `bun:"phase_deadline_at"`
	ThemeID          *int64          `bun:"theme_id"`
	ThemeDrawn       bool            `bun:"theme_drawn,notnull"`
	WordID           *int64          `bun:"word_id"`
	SubjectPlayerID  *string         `bun:"subject_player_id,type:uuid"`
	ImpostorPlayerID *string         `bun:"impostor_player_id,type:uuid"`
	CurrentTurn      int             `bun:"current_turn,notnull"`
	ImpostorFound    *bool           `bun:"impostor_found"`
	GuessOptions     json.RawMessage `bun:"guess_options,type:jsonb,nullzero"`
	GuessWordID      *int64          `bun:"guess_word_id"`
	GuessPlayerID    *string         `bun:"guess_player_id,type:uuid"`
	GuessCorrect     *bool           `bun:"guess_correct"`
	CreatedAt        time.Time       `bun:"created_at,nullzero,notnull"`

	Word    *Word   `bun:"rel:belongs-to,join:word_id=id"`
	Subject *Player `bun:"rel:belongs-to,join:subject_player_id=id"`
}

// Turn is a row of the turns table. Question is loaded with Relation.
type Turn struct {
	bun.BaseModel `bun:"table:turns,alias:tn"`

	ID         string     `bun:"id,pk,type:uuid,nullzero"`
	RoundID    string     `bun:"round_id,type:uuid,notnull"`
	Position   int        `bun:"position,notnull"`
	AskerID    string     `bun:"asker_id,type:uuid,notnull"`
	AnswererID string     `bun:"answerer_id,type:uuid,notnull"`
	QuestionID int64      `bun:"question_id,notnull"`
	StartedAt  *time.Time `bun:"started_at"`
	DeadlineAt *time.Time `bun:"deadline_at"`
	EndedAt    *time.Time `bun:"ended_at"`
	EndedBy    *string    `bun:"ended_by"`

	Question *Question `bun:"rel:belongs-to,join:question_id=id"`
}

// Vote is a row of the votes table. A nil TargetID is an abstention.
type Vote struct {
	bun.BaseModel `bun:"table:votes,alias:v"`

	RoundID  string    `bun:"round_id,pk,type:uuid"`
	VoterID  string    `bun:"voter_id,pk,type:uuid"`
	TargetID *string   `bun:"target_id,type:uuid"`
	CastAt   time.Time `bun:"cast_at,nullzero,notnull"`
}

// RoundScore is a row of the round_scores table.
type RoundScore struct {
	bun.BaseModel `bun:"table:round_scores,alias:rs"`

	RoundID  string `bun:"round_id,pk,type:uuid"`
	PlayerID string `bun:"player_id,pk,type:uuid"`
	Reason   string `bun:"reason,pk"`
	Points   int    `bun:"points,notnull"`
}
