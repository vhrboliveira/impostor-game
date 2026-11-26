// Command seed loads the embedded catalog (themes, words and questions) into the database.
// It is idempotent: run it again after editing the JSON files in internal/content/data.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/vhrboliveira/impostor-game/internal/config"
	"github.com/vhrboliveira/impostor-game/internal/content"
	"github.com/vhrboliveira/impostor-game/internal/storage"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run() error {
	release := flag.Bool("release", false, "also enforce the content minimums (enough questions per theme)")
	flag.Parse()

	dsn, err := config.DatabaseURL(os.Getenv)
	if err != nil {
		return err
	}

	catalog, err := content.Load()
	if err != nil {
		return err
	}
	validate := catalog.Validate
	if *release {
		validate = catalog.ValidateForRelease
	}
	if err := validate(); err != nil {
		return fmt.Errorf("invalid catalog: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	db, err := storage.Open(ctx, dsn, logger)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if err := storage.SeedCatalog(ctx, db, catalog); err != nil {
		return err
	}

	fmt.Printf("seeded %d themes, %d words and %d questions\n",
		len(catalog.Themes), catalog.WordCount(), len(catalog.Questions))
	return nil
}
