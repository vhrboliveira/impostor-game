package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakePinger struct{ err error }

func (f fakePinger) PingContext(context.Context) error { return f.err }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func do(t *testing.T, handler http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, target, nil))
	return rec
}

func TestHealth(t *testing.T) {
	router := NewRouter(Deps{Logger: discardLogger(), DB: fakePinger{}})

	rec := do(t, router, http.MethodGet, "/healthz")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

func TestReady(t *testing.T) {
	tests := []struct {
		name       string
		pingErr    error
		wantStatus int
		wantBody   string
	}{
		{name: "database reachable", wantStatus: http.StatusOK, wantBody: `{"status":"ok"}`},
		{
			name:       "database unreachable",
			pingErr:    errors.New("connection refused"),
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `{"status":"unavailable"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := NewRouter(Deps{Logger: discardLogger(), DB: fakePinger{err: tt.pingErr}})

			rec := do(t, router, http.MethodGet, "/readyz")

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.JSONEq(t, tt.wantBody, rec.Body.String())
		})
	}
}

func TestUnknownRouteReturnsJSONNotFound(t *testing.T) {
	router := NewRouter(Deps{Logger: discardLogger(), DB: fakePinger{}})

	rec := do(t, router, http.MethodGet, "/nope")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.JSONEq(t, `{"code":"not_found"}`, rec.Body.String())
}

func TestRequestID(t *testing.T) {
	router := NewRouter(Deps{Logger: discardLogger(), DB: fakePinger{}})

	t.Run("generates one when missing", func(t *testing.T) {
		rec := do(t, router, http.MethodGet, "/healthz")

		assert.Len(t, rec.Header().Get("X-Request-Id"), 16)
	})

	t.Run("keeps a valid client id", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
		req.Header.Set("X-Request-Id", "client-id_42")
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		assert.Equal(t, "client-id_42", rec.Header().Get("X-Request-Id"))
	})

	t.Run("replaces an invalid client id", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
		req.Header.Set("X-Request-Id", "bad id\nwith newline")
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		assert.NotEqual(t, "bad id\nwith newline", rec.Header().Get("X-Request-Id"))
		assert.Len(t, rec.Header().Get("X-Request-Id"), 16)
	})
}

func TestRecovererTurnsPanicsIntoInternalErrors(t *testing.T) {
	logger := discardLogger()
	panicking := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
	handler := chain(panicking, requestID, accessLog(logger), recoverer(logger))

	rec := do(t, handler, http.MethodGet, "/")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.JSONEq(t, `{"code":"internal_error"}`, rec.Body.String())
}

func TestChainRunsTheFirstMiddlewareOutermost(t *testing.T) {
	var order []string
	tag := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	final := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { order = append(order, "handler") })

	do(t, chain(final, tag("first"), tag("second")), http.MethodGet, "/")

	assert.Equal(t, []string{"first", "second", "handler"}, order)
}

func TestStatusRecorder(t *testing.T) {
	inner := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: inner}

	t.Run("exposes the underlying writer", func(t *testing.T) {
		assert.True(t, rec.Unwrap() == http.ResponseWriter(inner))
	})

	t.Run("records the first status written", func(t *testing.T) {
		rec.WriteHeader(http.StatusTeapot)
		rec.WriteHeader(http.StatusOK)

		assert.Equal(t, http.StatusTeapot, rec.status)
	})

	t.Run("fails to hijack when the underlying writer cannot", func(t *testing.T) {
		_, _, err := rec.Hijack()

		require.Error(t, err)
		assert.False(t, rec.hijacked)
	})
}
