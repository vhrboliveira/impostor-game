package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/vhrboliveira/impostor-game/internal/room"
)

// SetAvatar stores the player's photo, replacing the previous one.
func (r *Rooms) SetAvatar(ctx context.Context, playerID string, image []byte, at time.Time) error {
	if _, err := r.db.NewInsert().
		Model(&PlayerAvatar{PlayerID: playerID, Image: image, UpdatedAt: at}).
		On("CONFLICT (player_id) DO UPDATE").
		Set("image = EXCLUDED.image").
		Set("updated_at = EXCLUDED.updated_at").
		Exec(ctx); err != nil {
		return fmt.Errorf("set avatar: %w", err)
	}
	return nil
}

// DeleteAvatar removes the player's photo, if any.
func (r *Rooms) DeleteAvatar(ctx context.Context, playerID string) error {
	if _, err := r.db.NewDelete().Model((*PlayerAvatar)(nil)).
		Where("player_id = ?", playerID).
		Exec(ctx); err != nil {
		return fmt.Errorf("delete avatar: %w", err)
	}
	return nil
}

// LoadAvatar returns the photo of a player of the room identified by code.
func (r *Rooms) LoadAvatar(ctx context.Context, code, playerID string, now time.Time) (room.Avatar, error) {
	var row PlayerAvatar
	err := r.db.NewSelect().Model(&row).
		Join("JOIN players AS p ON p.id = pa.player_id").
		Join("JOIN rooms AS r ON r.id = p.room_id").
		Where("pa.player_id = ?", playerID).
		Where("r.code = ?", code).
		Where("r.expires_at > ?", now).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return room.Avatar{}, room.ErrAvatarNotFound
	}
	if err != nil {
		return room.Avatar{}, fmt.Errorf("read avatar: %w", err)
	}
	return room.Avatar{Image: row.Image, UpdatedAt: row.UpdatedAt}, nil
}
