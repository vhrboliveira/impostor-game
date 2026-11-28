// Package httpapi exposes the REST API and the HTTP plumbing (router and middleware) built
// on the standard library's net/http.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
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
	Rooms  RoomService
	// Avatars serves the player photo endpoints. They are only registered when it is set.
	Avatars AvatarService
	// Realtime serves GET /ws/room/{code}. The route is only registered when it is set.
	Realtime http.Handler
	// TrustedProxies are the networks of the reverse proxies in front of the API. Only requests
	// from them may name the client in X-Forwarded-For (rate limits, access log).
	TrustedProxies []netip.Prefix
	// Limits are the per-client rate limits; zero means DefaultLimits.
	Limits Limits
}

// NewRouter builds the HTTP handler with all routes and middleware.
func NewRouter(deps Deps) http.Handler {
	limits := deps.Limits
	if limits == (Limits{}) {
		limits = DefaultLimits
	}
	addr := clientAddress{trusted: deps.TrustedProxies}
	creates := newClientLimiter(limits.Create)
	requests := newClientLimiter(limits.Requests)
	misses := newClientLimiter(limits.Misses)
	// guarded endpoints take a room code: a bounded request volume, and a small budget of failed
	// lookups so codes cannot be guessed (the code is the only capability to join a room).
	guarded := func(h http.Handler) http.Handler {
		return limitRequests(requests, addr, limitMisses(misses, addr, h))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("GET /readyz", handleReady(deps.Logger, deps.DB))
	mux.Handle("POST /api/room", limitRequests(creates, addr, handleCreateRoom(deps.Logger, deps.Rooms)))
	mux.Handle("POST /api/room/{code}/join", guarded(handleJoinRoom(deps.Logger, deps.Rooms)))
	if deps.Avatars != nil {
		mux.Handle("PUT /api/room/{code}/me/avatar", guarded(handleSetAvatar(deps.Logger, deps.Avatars)))
		mux.Handle("DELETE /api/room/{code}/me/avatar", guarded(handleDeleteAvatar(deps.Logger, deps.Avatars)))
		mux.HandleFunc("GET /api/room/{code}/players/{id}/avatar", handleGetAvatar(deps.Logger, deps.Avatars))
	}
	if deps.Realtime != nil {
		// The handshake answers 101 before the token is checked, so this layer cannot see a miss;
		// the WebSocket closes with 4401 alike for an unknown room and a wrong token instead.
		mux.Handle("GET /ws/room/{code}", limitRequests(requests, addr, deps.Realtime))
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found")
	})

	return chain(mux, requestID, accessLog(deps.Logger, addr), recoverer(deps.Logger))
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
