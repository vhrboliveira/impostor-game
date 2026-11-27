package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHTTPServer_BoundsEveryRequest(t *testing.T) {
	s := newHTTPServer(":0", http.NotFoundHandler(), defaultServerTimeouts)

	assert.Equal(t, 5*time.Second, s.ReadHeaderTimeout)
	assert.Equal(t, 30*time.Second, s.ReadTimeout, "a slow body cannot hold a request forever")
	assert.Equal(t, 30*time.Second, s.WriteTimeout, "an unread response cannot hold a request forever")
	assert.Equal(t, 2*time.Minute, s.IdleTimeout)
}

// The server-wide timeouts end a stalled request, while a WebSocket outlives them: net/http clears
// the connection deadlines when the handler hijacks it.
func TestNewHTTPServer_TimeoutsSpareWebSockets(t *testing.T) {
	timeouts := serverTimeouts{ReadHeader: time.Second, Read: 200 * time.Millisecond, Write: 200 * time.Millisecond, Idle: time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /upload", func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1<<10)
		for {
			if _, err := r.Body.Read(buf); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
	})
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.CloseNow() }()
		for {
			typ, data, err := ws.Read(r.Context())
			if err != nil {
				return
			}
			if ws.Write(r.Context(), typ, data) != nil {
				return
			}
		}
	})
	srv := httptest.NewUnstartedServer(nil)
	srv.Config = newHTTPServer("", mux, timeouts)
	srv.Start()
	t.Cleanup(srv.Close)

	t.Run("stalled body", func(t *testing.T) {
		var dialer net.Dialer
		conn, err := dialer.DialContext(t.Context(), "tcp", srv.Listener.Addr().String())
		require.NoError(t, err)
		defer func() { _ = conn.Close() }()
		_, err = conn.Write([]byte("PUT /upload HTTP/1.1\r\nHost: x\r\nContent-Length: 1000\r\n\r\npartial"))
		require.NoError(t, err)

		require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
		start := time.Now()
		buf := make([]byte, 512)
		_, _ = conn.Read(buf) // the response, or EOF when the server gives up
		assert.Less(t, time.Since(start), 2*time.Second, "the server ends the stalled request")
	})

	t.Run("websocket", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ws, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", nil)
		require.NoError(t, err)
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		defer func() { _ = ws.CloseNow() }()

		time.Sleep(3 * timeouts.Read)
		require.NoError(t, ws.Write(ctx, websocket.MessageText, []byte("ping")))
		_, data, err := ws.Read(ctx)
		require.NoError(t, err)
		assert.Equal(t, "ping", string(data))
	})
}
