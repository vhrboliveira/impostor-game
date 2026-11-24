// Package storage is the PostgreSQL layer, built on the bun ORM. It owns the connection pool,
// the database models, the seed of the catalog and the repositories. The schema itself is
// managed by the SQL migrations in the migrations subpackage.
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

const (
	maxOpenConns    = 25
	maxIdleConns    = 5
	connMaxLifetime = 30 * time.Minute
	pingTimeout     = 5 * time.Second
)

// Open creates the connection pool and checks that the database answers. When the logger has
// the debug level enabled, every SQL query is logged.
func Open(ctx context.Context, dsn string, logger *slog.Logger) (*bun.DB, error) {
	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
	sqldb.SetMaxOpenConns(maxOpenConns)
	sqldb.SetMaxIdleConns(maxIdleConns)
	sqldb.SetConnMaxLifetime(connMaxLifetime)

	db := bun.NewDB(sqldb, pgdialect.New())
	if logger.Enabled(ctx, slog.LevelDebug) {
		db = db.WithQueryHook(queryLogger{logger: logger})
	}

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return db, nil
}
