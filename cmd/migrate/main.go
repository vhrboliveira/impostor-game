// Command migrate manages the database schema with bun's migrator.
//
// Usage:
//
//	migrate init              create the migration bookkeeping tables (idempotent)
//	migrate up                apply all pending migrations
//	migrate down              roll back the last group of migrations
//	migrate status            show applied and pending migrations
//	migrate create [-dir d] n create the up/down SQL files for a new migration named n
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/uptrace/bun/migrate"

	"github.com/vhrboliveira/impostor-game/internal/config"
	"github.com/vhrboliveira/impostor-game/internal/storage"
	"github.com/vhrboliveira/impostor-game/internal/storage/migrations"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		printUsage(out)
		return errors.New("missing command")
	}

	switch command := args[0]; command {
	case "create":
		return runCreate(args[1:], out, time.Now)
	case "init", "up", "down", "status":
		return runDatabase(ctx, command, out)
	case "help", "-h", "--help":
		printUsage(out)
		return nil
	default:
		printUsage(out)
		return fmt.Errorf("unknown command %q", command)
	}
}

func printUsage(out io.Writer) {
	fmt.Fprint(out, `Usage: migrate <command>

Commands:
  init               create the migration bookkeeping tables (idempotent)
  up                 apply all pending migrations
  down               roll back the last group of migrations
  status             show applied and pending migrations
  create [-dir d] n  create the up/down SQL files for a new migration named n

Database commands read DATABASE_URL from the environment.
`)
}

func runDatabase(ctx context.Context, command string, out io.Writer) error {
	dsn, err := config.DatabaseURL(os.Getenv)
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	db, err := storage.Open(ctx, dsn, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	migrator := migrate.NewMigrator(db, migrations.Migrations)
	if err := migrator.Init(ctx); err != nil {
		return fmt.Errorf("init migration tables: %w", err)
	}

	switch command {
	case "init":
		fmt.Fprintln(out, "migration tables are ready")

	case "up":
		if err := migrator.Lock(ctx); err != nil {
			return fmt.Errorf("lock migrations: %w", err)
		}
		defer func() { _ = migrator.Unlock(context.WithoutCancel(ctx)) }()

		group, err := migrator.Migrate(ctx)
		if err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		if group.IsZero() {
			fmt.Fprintln(out, "no new migrations to run (database is up to date)")
			return nil
		}
		fmt.Fprintf(out, "migrated to %s\n", group)

	case "down":
		if err := migrator.Lock(ctx); err != nil {
			return fmt.Errorf("lock migrations: %w", err)
		}
		defer func() { _ = migrator.Unlock(context.WithoutCancel(ctx)) }()

		group, err := migrator.Rollback(ctx)
		if err != nil {
			return fmt.Errorf("rollback: %w", err)
		}
		if group.IsZero() {
			fmt.Fprintln(out, "there are no groups to roll back")
			return nil
		}
		fmt.Fprintf(out, "rolled back %s\n", group)

	case "status":
		ms, err := migrator.MigrationsWithStatus(ctx)
		if err != nil {
			return fmt.Errorf("read migration status: %w", err)
		}
		fmt.Fprintf(out, "migrations: %s\n", ms)
		fmt.Fprintf(out, "unapplied migrations: %s\n", ms.Unapplied())
		fmt.Fprintf(out, "last migration group: %s\n", ms.LastGroup())
	}
	return nil
}
