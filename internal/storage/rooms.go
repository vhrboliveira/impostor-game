package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

// Rooms is the repository of a room and everything that hangs from it: players, matches,
// rounds and turns. It implements room.Store (lobby.go), room.LiveStore (live.go) and the
// janitor's cleanup (this file).
type Rooms struct {
	db *bun.DB
}

// NewRooms creates the rooms repository.
func NewRooms(db *bun.DB) *Rooms {
	return &Rooms{db: db}
}

// DeleteExpired deletes the rooms past their maximum lifetime (expires_at) and the rooms that
// have been empty for longer than emptyTTL. Players, matches, rounds and the rest of the game
// are removed by ON DELETE CASCADE. It returns how many rooms were deleted.
func (r *Rooms) DeleteExpired(ctx context.Context, now time.Time, emptyTTL time.Duration) (int64, error) {
	res, err := r.db.NewDelete().
		Model((*Room)(nil)).
		Where("expires_at <= ?", now).
		WhereOr("empty_since IS NOT NULL AND empty_since <= ?", now.Add(-emptyTTL)).
		Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("delete expired rooms: %w", err)
	}

	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted rooms: %w", err)
	}
	return deleted, nil
}
