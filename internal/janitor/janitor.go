// Package janitor periodically deletes rooms that are past their lifetime: rooms older than
// the maximum age and rooms that have been empty for too long.
package janitor

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// DefaultInterval is how often the janitor looks for expired rooms.
const DefaultInterval = time.Minute

// Store deletes expired rooms. storage.Rooms implements it.
type Store interface {
	DeleteExpired(ctx context.Context, now time.Time, emptyTTL time.Duration) (int64, error)
}

// Janitor runs the cleanup on a fixed interval.
type Janitor struct {
	store    Store
	logger   *slog.Logger
	interval time.Duration
	emptyTTL time.Duration
	now      func() time.Time
}

// New creates a Janitor. emptyTTL is how long a room may stay empty before being deleted.
func New(store Store, logger *slog.Logger, interval, emptyTTL time.Duration) *Janitor {
	return &Janitor{
		store:    store,
		logger:   logger,
		interval: interval,
		emptyTTL: emptyTTL,
		now:      time.Now,
	}
}

// RunOnce deletes the expired rooms right now.
func (j *Janitor) RunOnce(ctx context.Context) error {
	deleted, err := j.store.DeleteExpired(ctx, j.now(), j.emptyTTL)
	if err != nil {
		return fmt.Errorf("delete expired rooms: %w", err)
	}
	if deleted > 0 {
		j.logger.Info("expired rooms deleted", slog.Int64("count", deleted))
	}
	return nil
}

// Run cleans up immediately and then on every tick until ctx is canceled.
func (j *Janitor) Run(ctx context.Context) {
	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()

	for {
		if err := j.RunOnce(ctx); err != nil && ctx.Err() == nil {
			j.logger.Error("room cleanup failed", slog.Any("error", err))
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
