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

// MarkAllEmpty starts the empty-room TTL of every room still marked occupied (empty_since NULL).
// It runs once at startup, before the server accepts connections: no WebSocket survives a restart
// of the single API process, so such a marker was left by a crash or an interrupted shutdown.
func (r *Rooms) MarkAllEmpty(ctx context.Context, now time.Time) error {
	if _, err := r.db.NewUpdate().Model((*Room)(nil)).
		Set("empty_since = ?", now).
		Where("empty_since IS NULL").
		Exec(ctx); err != nil {
		return fmt.Errorf("mark rooms empty: %w", err)
	}
	return nil
}

// NewRooms creates the rooms repository.
func NewRooms(db *bun.DB) *Rooms {
	return &Rooms{db: db}
}

// DeleteExpired deletes at most limit of the rooms past their maximum lifetime (expires_at) or
// empty for longer than emptyTTL. Players, matches, rounds and the rest of the game are removed
// by ON DELETE CASCADE. It returns how many rooms were deleted.
func (r *Rooms) DeleteExpired(ctx context.Context, now time.Time, emptyTTL time.Duration, limit int) (int64, error) {
	batch := r.db.NewSelect().Model((*Room)(nil)).
		Column("id").
		Where("expires_at <= ?", now).
		WhereOr("empty_since IS NOT NULL AND empty_since <= ?", now.Add(-emptyTTL)).
		Limit(int64(limit))
	res, err := r.db.NewDelete().
		Model((*Room)(nil)).
		Where("id IN (?)", batch).
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
