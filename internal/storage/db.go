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

// MinServerVersion is the oldest PostgreSQL major version the schema and the queries are written
// for. Open refuses older servers.
const MinServerVersion = 17

const (
	maxOpenConns    = 25
	maxIdleConns    = 5
	connMaxLifetime = 30 * time.Minute
	pingTimeout     = 5 * time.Second
)

// Open creates the connection pool and checks that the database answers and runs PostgreSQL
// MinServerVersion or newer. When the logger has the debug level enabled, every SQL query is
// logged.
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

	var versionNum int
	if err := db.NewSelect().ColumnExpr("current_setting('server_version_num')::int").Scan(pingCtx, &versionNum); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("read server version: %w", err)
	}
	if err := requireServerVersion(versionNum); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// requireServerVersion checks a server_version_num (for example 170012 for 17.12).
func requireServerVersion(versionNum int) error {
	if major := versionNum / 10000; major < MinServerVersion {
		return fmt.Errorf("PostgreSQL %d or newer is required, the server runs version %d", MinServerVersion, major)
	}
	return nil
}
