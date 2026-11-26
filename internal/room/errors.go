package room

import "errors"

// Errors returned by the Service. The HTTP layer maps each one to a stable API error code.
var (
	ErrInvalidName     = errors.New("invalid player name")
	ErrInvalidRounds   = errors.New("invalid number of rounds")
	ErrRoomNotFound    = errors.New("room not found")
	ErrRoomFull        = errors.New("room is full")
	ErrMatchInProgress = errors.New("a round is in progress")
	ErrMatchFinished   = errors.New("the match is over")
	// ErrUnauthorized means a session token does not belong to any player of the room.
	ErrUnauthorized = errors.New("invalid session token")
	// ErrInvalidAvatar means an uploaded photo is not a JPEG or PNG within the size limits.
	ErrInvalidAvatar = errors.New("invalid avatar image")
	// ErrAvatarLocked means the photo can no longer change: a player without a photo can send
	// one at any moment, but changing or removing it is only allowed in the lobby.
	ErrAvatarLocked = errors.New("the avatar can only change in the lobby")
	// ErrAvatarNotFound means the player has no photo (or the room or player does not exist).
	ErrAvatarNotFound = errors.New("avatar not found")

	// ErrCodeAllocation and ErrNameAllocation mean the service gave up after several attempts.
	// They are unexpected and surface as internal errors.
	ErrCodeAllocation = errors.New("could not allocate a free room code")
	ErrNameAllocation = errors.New("could not allocate a free player name")
)

// Errors reported by a Store for conflicts the Service resolves by retrying. They never reach
// the API.
var (
	ErrCodeTaken = errors.New("room code already in use")
	ErrNameTaken = errors.New("player name already in use")
)
