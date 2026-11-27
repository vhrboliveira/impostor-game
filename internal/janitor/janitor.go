// Package janitor periodically deletes rooms that are past their lifetime: rooms older than
// the maximum age and rooms that have been empty for too long.
package janitor

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

const (
	// DefaultInterval is how often the janitor looks for expired rooms.
	DefaultInterval = time.Minute
	// DefaultBatchSize bounds the rooms deleted by one statement, so a large cleanup does not hold
	// row locks (and the cascades) for the whole set at once.
	DefaultBatchSize = 500
	// defaultRunTimeout bounds one cleanup run, so a hung database does not stall it forever.
	defaultRunTimeout = 30 * time.Second
)

// Store deletes expired rooms, at most limit per call. storage.Rooms implements it.
type Store interface {
	DeleteExpired(ctx context.Context, now time.Time, emptyTTL time.Duration, limit int) (int64, error)
}

// Janitor runs the cleanup on a fixed interval.
type Janitor struct {
	store    Store
	logger   *slog.Logger
	interval time.Duration
	emptyTTL time.Duration
	now      func() time.Time
	// runTimeout bounds one RunOnce.
	runTimeout time.Duration
}

// New creates a Janitor. emptyTTL is how long a room may stay empty before being deleted.
func New(store Store, logger *slog.Logger, interval, emptyTTL time.Duration) *Janitor {
	return &Janitor{
		store:      store,
		logger:     logger,
		interval:   interval,
		emptyTTL:   emptyTTL,
		now:        time.Now,
		runTimeout: defaultRunTimeout,
	}
}

// RunOnce deletes the expired rooms right now, in batches of DefaultBatchSize until a batch comes
// back short, within runTimeout.
func (j *Janitor) RunOnce(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, j.runTimeout)
	defer cancel()

	now := j.now()
	var total int64
	for {
		deleted, err := j.store.DeleteExpired(ctx, now, j.emptyTTL, DefaultBatchSize)
		total += deleted
		if err != nil {
			if total > 0 {
				j.logger.Info("expired rooms deleted", slog.Int64("count", total))
			}
			return fmt.Errorf("delete expired rooms: %w", err)
		}
		if deleted < DefaultBatchSize {
			break
		}
	}
	if total > 0 {
		j.logger.Info("expired rooms deleted", slog.Int64("count", total))
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
