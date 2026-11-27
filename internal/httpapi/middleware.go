package httpapi

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"slices"
	"time"
)

// Middleware wraps a handler with cross-cutting behavior.
type Middleware func(http.Handler) http.Handler

// chain applies middlewares so that the first one listed is the outermost.
func chain(h http.Handler, middlewares ...Middleware) http.Handler {
	for _, middleware := range slices.Backward(middlewares) {
		h = middleware(h)
	}
	return h
}

type requestIDKey struct{}

// RequestIDFromContext returns the request id set by the requestID middleware.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

const maxRequestIDLength = 64

func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLength {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// requestID keeps a sane client-provided X-Request-Id or generates one, exposes it in the
// response header and stores it in the request context.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !validRequestID(id) {
			id = newRequestID()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// statusRecorder captures the response status for logging. It forwards Hijack and Flush and
// exposes Unwrap, so WebSocket upgrades and streaming keep working through the middleware.
type statusRecorder struct {
	http.ResponseWriter
	status   int
	hijacked bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("httpapi: underlying ResponseWriter does not support hijacking")
	}
	conn, rw, err := h.Hijack()
	if err == nil {
		r.hijacked = true
	}
	return conn, rw, err
}

func isHealthPath(path string) bool {
	return path == "/healthz" || path == "/readyz"
}

// accessLog logs one line per request. Health probes are logged at debug level to keep the
// output readable.
func accessLog(logger *slog.Logger, addr clientAddress) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}

			next.ServeHTTP(rec, r)

			status := rec.status
			switch {
			case rec.hijacked:
				status = http.StatusSwitchingProtocols
			case status == 0:
				status = http.StatusOK
			}

			level := slog.LevelInfo
			switch {
			case status >= http.StatusInternalServerError:
				level = slog.LevelError
			case isHealthPath(r.URL.Path):
				level = slog.LevelDebug
			}

			logger.LogAttrs(r.Context(), level, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Duration("duration", time.Since(start)),
				slog.String("request_id", RequestIDFromContext(r.Context())),
				slog.String("remote_addr", r.RemoteAddr),
				slog.String("client", addr.of(r)),
			)
		})
	}
}

// recoverer turns a panic in a handler into a 500 response instead of dropping the connection.
func recoverer(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}
				logger.ErrorContext(r.Context(), "panic recovered",
					slog.Any("panic", rec),
					slog.String("stack", string(debug.Stack())),
					slog.String("request_id", RequestIDFromContext(r.Context())),
				)
				writeError(w, http.StatusInternalServerError, "internal_error")
			}()
			next.ServeHTTP(w, r)
		})
	}
}
