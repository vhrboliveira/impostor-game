package room

import (
	"context"
	"errors"
	"time"

	"github.com/vhrboliveira/impostor-game/internal/game"
)

// SetAvatar authenticates the player by their session token and sets their photo. The image is
// validated and re-encoded before reaching the room (NormalizeAvatar). The change goes through
// the room's actor, which applies the rule (a first photo at any moment, a change only in the
// lobby) and sends everyone a snapshot with the new avatar URL. It returns ErrRoomNotFound,
// ErrUnauthorized, ErrInvalidAvatar or ErrAvatarLocked.
func (m *Manager) SetAvatar(ctx context.Context, rawCode, token string, image []byte) error {
	normalized, err := NormalizeAvatar(image)
	if err != nil {
		return err
	}
	return m.withPlayer(ctx, rawCode, token, func(a *actor, playerID string) error {
		return a.setAvatar(playerID, normalized)
	})
}

// DeleteAvatar removes the player's photo. It is only allowed in the lobby (ErrAvatarLocked).
func (m *Manager) DeleteAvatar(ctx context.Context, rawCode, token string) error {
	return m.withPlayer(ctx, rawCode, token, func(a *actor, playerID string) error {
		return a.deleteAvatar(playerID)
	})
}

// Avatar returns the photo of a player of the room, or ErrAvatarNotFound. It reads the database
// directly: photos are not part of the actor's state.
func (m *Manager) Avatar(ctx context.Context, rawCode, playerID string) (Avatar, error) {
	code, ok := NormalizeCode(rawCode)
	if !ok || !validPlayerID(playerID) {
		return Avatar{}, ErrAvatarNotFound
	}
	return m.store.LoadAvatar(ctx, code, playerID, m.clock.Now())
}

// withPlayer authenticates the player and runs fn on the room's actor, starting one if nobody is
// connected (a photo is usually sent right after joining, before the WebSocket opens).
func (m *Manager) withPlayer(ctx context.Context, rawCode, token string, fn func(a *actor, playerID string) error) error {
	code, ok := NormalizeCode(rawCode)
	if !ok {
		return ErrRoomNotFound
	}
	if token == "" {
		return ErrUnauthorized
	}
	playerID, err := m.store.Authenticate(ctx, code, HashToken(token), m.clock.Now())
	if err != nil {
		return err
	}

	for range maxConnectAttempts {
		a, err := m.actorFor(ctx, code)
		if err != nil {
			return err
		}
		err = a.call(func() error { return fn(a, playerID) })
		if errors.Is(err, errActorStopped) {
			continue
		}
		return err
	}
	return errActorStopped
}

// call runs fn on the actor and waits for its result. It returns errActorStopped when the actor
// is gone.
func (a *actor) call(fn func() error) error {
	reply := make(chan error, 1)
	if !a.do(func() { reply <- fn() }) {
		return errActorStopped
	}
	return <-reply
}

func (a *actor) setAvatar(playerID string, image []byte) error {
	p, err := a.knownPlayer(playerID)
	if err != nil {
		return err
	}
	if p.AvatarAt != nil && a.state.Match.Status != game.MatchLobby {
		return ErrAvatarLocked
	}

	// PostgreSQL keeps microseconds: the version read back after a reload must be the same.
	at := a.m.clock.Now().Truncate(time.Microsecond)
	ctx, cancel := storeContext()
	defer cancel()
	if err := a.m.store.SetAvatar(ctx, playerID, image, at); err != nil {
		return err
	}
	p.AvatarAt = &at
	a.broadcast()
	return nil
}

func (a *actor) deleteAvatar(playerID string) error {
	p, err := a.knownPlayer(playerID)
	if err != nil {
		return err
	}
	if a.state.Match.Status != game.MatchLobby {
		return ErrAvatarLocked
	}
	if p.AvatarAt == nil {
		return nil
	}

	ctx, cancel := storeContext()
	defer cancel()
	if err := a.m.store.DeleteAvatar(ctx, playerID); err != nil {
		return err
	}
	p.AvatarAt = nil
	a.broadcast()
	return nil
}

// knownPlayer returns the player, re-reading the players when they joined over REST after this
// actor loaded the room.
func (a *actor) knownPlayer(playerID string) (*PlayerState, error) {
	if p := a.player(playerID); p != nil {
		return p, nil
	}
	if err := a.reloadPlayers(); err != nil {
		return nil, err
	}
	if p := a.player(playerID); p != nil {
		return p, nil
	}
	return nil, ErrUnauthorized
}
