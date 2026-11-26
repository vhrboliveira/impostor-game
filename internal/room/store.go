package room

import (
	"context"
	"time"
)

// Player is a participant of a room.
type Player struct {
	ID     string
	RoomID string
	Name   string
	IsHost bool
}

// CreateRoomParams describes a new room together with its first match and its host.
type CreateRoomParams struct {
	Code        string
	Now         time.Time
	ExpiresAt   time.Time
	TotalRounds int
	TurnSeconds int
	HostName    string
	// TokenHash is the SHA-256 of the host's session token; the token itself is never stored.
	TokenHash string
}

// AddPlayerParams describes a player joining an existing room.
type AddPlayerParams struct {
	Code       string
	Name       string
	TokenHash  string
	MaxPlayers int
	// Now is used to ignore rooms that already expired but were not cleaned up yet.
	Now time.Time
}

// Store is the persistence the Service needs. Implementations must be safe for concurrent use
// and apply each method atomically.
type Store interface {
	// CreateRoom creates the room, its first match (in the lobby) and the host player in one
	// transaction. It returns ErrCodeTaken when the code is already in use.
	CreateRoom(ctx context.Context, params CreateRoomParams) (Player, error)

	// AddPlayer adds a player to the room identified by params.Code. It returns ErrRoomNotFound,
	// ErrRoomFull, ErrMatchInProgress (a round is running), ErrMatchFinished (the match is over
	// and no new one was started) or ErrNameTaken.
	AddPlayer(ctx context.Context, params AddPlayerParams) (Player, error)
}
