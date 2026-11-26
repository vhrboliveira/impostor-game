package room

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vhrboliveira/impostor-game/internal/game"
)

const (
	maxCodeAttempts = 10
	maxNameAttempts = 50
)

// Config holds the settings of the Service.
type Config struct {
	// RoomTTL is the maximum lifetime of a room, counted from its creation.
	RoomTTL time.Duration
}

// CreateRoomInput is the data needed to create a room. The creator becomes the host.
type CreateRoomInput struct {
	PlayerName  string
	TotalRounds int
}

// JoinRoomInput is the data needed to join a room.
type JoinRoomInput struct {
	PlayerName string
}

// Session identifies a player inside a room. The token authenticates the player's WebSocket
// connection and must be kept on the device; the server only stores its hash.
type Session struct {
	RoomCode string
	PlayerID string
	Token    string
}

// Service implements the room lifecycle: creating rooms and joining them.
type Service struct {
	store Store
	cfg   Config

	// Injected so tests are deterministic.
	now      func() time.Time
	newCode  func() (string, error)
	newToken func() (string, error)
}

// NewService creates a Service.
func NewService(store Store, cfg Config) *Service {
	return &Service{
		store:    store,
		cfg:      cfg,
		now:      time.Now,
		newCode:  NewCode,
		newToken: NewToken,
	}
}

// CreateRoom creates a room with its first match in the lobby and makes the caller its host.
func (s *Service) CreateRoom(ctx context.Context, in CreateRoomInput) (Session, error) {
	name, err := NormalizeName(in.PlayerName)
	if err != nil {
		return Session{}, err
	}
	if !game.ValidRounds(in.TotalRounds) {
		return Session{}, ErrInvalidRounds
	}

	token, err := s.newToken()
	if err != nil {
		return Session{}, fmt.Errorf("generate token: %w", err)
	}
	now := s.now()

	for range maxCodeAttempts {
		code, err := s.newCode()
		if err != nil {
			return Session{}, fmt.Errorf("generate room code: %w", err)
		}

		player, err := s.store.CreateRoom(ctx, CreateRoomParams{
			Code:        code,
			Now:         now,
			ExpiresAt:   now.Add(s.cfg.RoomTTL),
			TotalRounds: in.TotalRounds,
			TurnSeconds: game.DefaultTurnSeconds,
			HostName:    name,
			TokenHash:   HashToken(token),
		})
		if errors.Is(err, ErrCodeTaken) {
			continue
		}
		if err != nil {
			return Session{}, fmt.Errorf("create room: %w", err)
		}
		return Session{RoomCode: code, PlayerID: player.ID, Token: token}, nil
	}
	return Session{}, ErrCodeAllocation
}

// JoinRoom adds a player to an existing room. When the name is already taken in the room a
// number is appended ("Ana 2").
func (s *Service) JoinRoom(ctx context.Context, rawCode string, in JoinRoomInput) (Session, error) {
	code, ok := NormalizeCode(rawCode)
	if !ok {
		return Session{}, ErrRoomNotFound
	}
	name, err := NormalizeName(in.PlayerName)
	if err != nil {
		return Session{}, err
	}

	token, err := s.newToken()
	if err != nil {
		return Session{}, fmt.Errorf("generate token: %w", err)
	}

	for attempt := 1; attempt <= maxNameAttempts; attempt++ {
		player, err := s.store.AddPlayer(ctx, AddPlayerParams{
			Code:       code,
			Name:       nameCandidate(name, attempt),
			TokenHash:  HashToken(token),
			MaxPlayers: game.MaxPlayers,
			Now:        s.now(),
		})
		if errors.Is(err, ErrNameTaken) {
			continue
		}
		if err != nil {
			return Session{}, fmt.Errorf("add player: %w", err)
		}
		return Session{RoomCode: code, PlayerID: player.ID, Token: token}, nil
	}
	return Session{}, ErrNameAllocation
}
