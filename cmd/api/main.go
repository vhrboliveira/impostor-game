// Command api runs the HTTP and WebSocket server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vhrboliveira/impostor-game/internal/config"
	"github.com/vhrboliveira/impostor-game/internal/httpapi"
	"github.com/vhrboliveira/impostor-game/internal/janitor"
	"github.com/vhrboliveira/impostor-game/internal/storage"
)

const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "api:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	logger := newLogger(cfg)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := storage.Open(ctx, cfg.DatabaseURL, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	go janitor.New(storage.NewRooms(db), logger, janitor.DefaultInterval, cfg.EmptyRoomTTL).Run(ctx)

	// Read and write timeouts are intentionally unset: WebSocket connections are long-lived and
	// REST handlers bound their own work with contexts.
	server := &http.Server{
		Addr:              net.JoinHostPort("", cfg.Port),
		Handler:           httpapi.NewRouter(httpapi.Deps{Logger: logger, DB: db}),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("server listening", slog.String("addr", server.Addr), slog.String("env", cfg.Env))
		serverErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("server: %w", err)
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// newLogger returns JSON logs in production and human-friendly text logs otherwise.
func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.IsProd() {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
