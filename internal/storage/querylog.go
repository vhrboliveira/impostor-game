package storage

import (
	"context"
	"log/slog"
	"time"

	"github.com/uptrace/bun"
)

// queryLogger is a bun hook that logs every query at debug level.
type queryLogger struct {
	logger *slog.Logger
}

func (queryLogger) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}

func (h queryLogger) AfterQuery(ctx context.Context, event *bun.QueryEvent) {
	h.logger.LogAttrs(ctx, slog.LevelDebug, "sql query",
		slog.String("query", event.Query),
		slog.Duration("duration", time.Since(event.StartTime)),
		slog.Any("error", event.Err),
	)
}
