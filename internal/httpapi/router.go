// Package httpapi exposes the REST API and the HTTP plumbing (router and middleware) built
// on the standard library's net/http.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// readyTimeout bounds the database check of the readiness probe.
const readyTimeout = 2 * time.Second

// Pinger reports whether the database is reachable. *bun.DB implements it.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// Deps are the collaborators the router needs.
type Deps struct {
	Logger *slog.Logger
	DB     Pinger
}

// NewRouter builds the HTTP handler with all routes and middleware.
func NewRouter(deps Deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("GET /readyz", handleReady(deps.Logger, deps.DB))
	// TODO(next step): POST /api/room, POST /api/room/{code}/join and GET /ws/room/{code}.
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found")
	})

	return chain(mux, requestID, accessLog(deps.Logger), recoverer(deps.Logger))
}

// handleHealth is the liveness probe: the process is up.
func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}

// handleReady is the readiness probe: the process can reach its database.
func handleReady(logger *slog.Logger, db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()

		if err := db.PingContext(ctx); err != nil {
			logger.WarnContext(r.Context(), "readiness check failed", slog.Any("error", err))
			writeJSON(w, http.StatusServiceUnavailable, statusBody{Status: "unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
	}
}
