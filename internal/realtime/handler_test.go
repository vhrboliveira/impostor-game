package realtime

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vhrboliveira/impostor-game/internal/protocol"
	"github.com/vhrboliveira/impostor-game/internal/room"
)

const testOrigin = "http://localhost:5173"

// fakeRooms accepts token "good" in room K7M2P and sends a snapshot on connect, like an actor.
type fakeRooms struct {
	mu       sync.Mutex
	client   room.Client
	handled  []protocol.ClientMessage
	left     chan struct{}
	attached chan struct{}
}

func newFakeRooms() *fakeRooms {
	return &fakeRooms{left: make(chan struct{}), attached: make(chan struct{})}
}

func (f *fakeRooms) Connect(_ context.Context, code, token string, client room.Client) (Session, error) {
	if code != "K7M2P" {
		return nil, room.ErrRoomNotFound
	}
	if token != "good" {
		return nil, room.ErrUnauthorized
	}
	f.mu.Lock()
	f.client = client
	f.mu.Unlock()
	client.Send(protocol.SnapshotMessage(protocol.RoomSnapshot{RoomCode: "K7M2P", Players: []protocol.PlayerView{}}))
	close(f.attached)
	return &fakeSession{rooms: f}, nil
}

func (f *fakeRooms) handledMessages() []protocol.ClientMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]protocol.ClientMessage(nil), f.handled...)
}

type fakeSession struct{ rooms *fakeRooms }

func (s *fakeSession) Handle(msg protocol.ClientMessage) {
	s.rooms.mu.Lock()
	defer s.rooms.mu.Unlock()
	s.rooms.handled = append(s.rooms.handled, msg)
}

func (s *fakeSession) Leave() { close(s.rooms.left) }

type testServer struct {
	url      string
	rooms    *fakeRooms
	shutdown context.CancelFunc
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	rooms := newFakeRooms()
	shutdownCtx, shutdown := context.WithCancel(context.Background())
	t.Cleanup(shutdown)

	handler := NewHandler(rooms, slog.New(slog.NewTextHandler(io.Discard, nil)), []string{testOrigin}, shutdownCtx)
	mux := http.NewServeMux()
	mux.Handle("GET /ws/room/{code}", handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return &testServer{url: "ws" + strings.TrimPrefix(server.URL, "http"), rooms: rooms, shutdown: shutdown}
}

// dial opens a WebSocket and returns the HTTP status of the handshake response.
func dial(t *testing.T, url, origin string) (*websocket.Conn, int, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	status := 0
	if resp != nil {
		status = resp.StatusCode
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}
	if conn != nil {
		t.Cleanup(func() { _ = conn.CloseNow() })
	}
	return conn, status, err
}

func readMessage(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	require.NoError(t, err)
	var msg map[string]any
	require.NoError(t, json.Unmarshal(data, &msg))
	return msg
}

func writeText(t *testing.T, conn *websocket.Conn, text string) {
	t.Helper()
	require.NoError(t, conn.Write(t.Context(), websocket.MessageText, []byte(text)))
}

// readCloseStatus reads until the server closes the connection and returns the close code.
func readCloseStatus(t *testing.T, conn *websocket.Conn) websocket.StatusCode {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			return websocket.CloseStatus(err)
		}
	}
}

func TestHandler_RejectsOtherOrigins(t *testing.T) {
	srv := newTestServer(t)

	for _, origin := range []string{"", "https://evil.example", "http://localhost:5174"} {
		t.Run("origin "+origin, func(t *testing.T) {
			_, status, err := dial(t, srv.url+"/ws/room/K7M2P?token=good", origin)

			require.Error(t, err)
			assert.Equal(t, http.StatusForbidden, status)
		})
	}
}

func TestHandler_ClosesWithFinalCodes(t *testing.T) {
	srv := newTestServer(t)

	cases := []struct {
		name string
		path string
		want websocket.StatusCode
	}{
		{name: "unknown room", path: "/ws/room/ZZZZZ?token=good", want: protocol.CloseRoomNotFound},
		{name: "bad token", path: "/ws/room/K7M2P?token=bad", want: protocol.CloseUnauthorized},
		{name: "missing token", path: "/ws/room/K7M2P", want: protocol.CloseUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, _, err := dial(t, srv.url+tc.path, testOrigin)
			require.NoError(t, err, "the upgrade succeeds so the close code can reach the browser")

			assert.Equal(t, tc.want, readCloseStatus(t, conn))
		})
	}
}

func TestHandler_ConnectedSession(t *testing.T) {
	srv := newTestServer(t)
	conn, _, err := dial(t, srv.url+"/ws/room/K7M2P?token=good", "HTTP://LOCALHOST:5173/")
	require.NoError(t, err, "origins compare case-insensitively, ignoring a trailing slash")

	first := readMessage(t, conn)
	assert.Equal(t, "room_snapshot", first["type"], "the snapshot is the first message")

	writeText(t, conn, `{"type":"ping"}`)
	assert.Equal(t, map[string]any{"type": "pong"}, readMessage(t, conn))

	writeText(t, conn, `{"type":"cheat"}`)
	assert.Equal(t, map[string]any{"type": "error", "payload": map[string]any{"code": "invalid_message"}}, readMessage(t, conn))

	require.NoError(t, conn.Write(t.Context(), websocket.MessageBinary, []byte{1, 2}))
	assert.Equal(t, "error", readMessage(t, conn)["type"])

	writeText(t, conn, `{"type":"start_match"}`)
	writeText(t, conn, `{"type":"ping"}`)
	readMessage(t, conn) // the pong proves start_match was processed before it
	assert.Equal(t, []protocol.ClientMessage{{Type: protocol.TypeStartMatch}}, srv.rooms.handledMessages())

	require.NoError(t, conn.Close(websocket.StatusNormalClosure, "bye"))
	select {
	case <-srv.rooms.left:
	case <-time.After(5 * time.Second):
		t.Fatal("the room was not told that the connection closed")
	}
}

func TestHandler_RoomCanCloseTheConnection(t *testing.T) {
	srv := newTestServer(t)
	conn, _, err := dial(t, srv.url+"/ws/room/K7M2P?token=good", testOrigin)
	require.NoError(t, err)
	readMessage(t, conn)

	srv.rooms.mu.Lock()
	srv.rooms.client.Close(protocol.CloseRoomNotFound, "room expired")
	srv.rooms.mu.Unlock()

	assert.Equal(t, websocket.StatusCode(protocol.CloseRoomNotFound), readCloseStatus(t, conn))
}

func TestHandler_ShutdownClosesWithGoingAway(t *testing.T) {
	srv := newTestServer(t)
	conn, _, err := dial(t, srv.url+"/ws/room/K7M2P?token=good", testOrigin)
	require.NoError(t, err)
	readMessage(t, conn)

	srv.shutdown()

	assert.Equal(t, websocket.StatusGoingAway, readCloseStatus(t, conn))
}

func TestHandler_RejectsOversizedMessages(t *testing.T) {
	srv := newTestServer(t)
	conn, _, err := dial(t, srv.url+"/ws/room/K7M2P?token=good", testOrigin)
	require.NoError(t, err)
	readMessage(t, conn)

	writeText(t, conn, `{"type":"ping","x":"`+strings.Repeat("a", protocol.MaxMessageBytes)+`"}`)

	assert.Equal(t, websocket.StatusMessageTooBig, readCloseStatus(t, conn))
}

func TestClient_SlowConnectionAsksToClose(t *testing.T) {
	c := newClient()
	for range outboundBuffer + 1 {
		c.Send(protocol.PongMessage())
	}

	select {
	case frame := <-c.closeReq:
		assert.Equal(t, int(websocket.StatusTryAgainLater), frame.code)
	default:
		t.Fatal("a full buffer should request a close")
	}

	c.Close(protocol.CloseRoomNotFound, "again") // later closes are ignored and never block
}

// Snapshots carry the whole room state, so a newer one replaces an unsent older one: a burst of
// room changes (another player's commands or reconnects) never fills a healthy connection's
// buffer and gets it closed.
func TestClient_SnapshotsReplaceEachOther(t *testing.T) {
	c := newClient()
	for i := range outboundBuffer * 3 {
		c.Send(protocol.SnapshotMessage(protocol.RoomSnapshot{RoomCode: "K7M2P", Players: []protocol.PlayerView{}, Me: protocol.MeView{}, Match: protocol.MatchView{RoundNumber: i}}))
	}

	select {
	case frame := <-c.closeReq:
		t.Fatalf("snapshots must not close the connection, got %d", frame.code)
	default:
	}
	msg, ok := c.takeSnapshot()
	require.True(t, ok)
	assert.Equal(t, outboundBuffer*3-1, msg.Payload.(protocol.RoomSnapshot).Match.RoundNumber, "only the newest is kept")
	_, ok = c.takeSnapshot()
	assert.False(t, ok)
}

// One connection cannot flood the room with commands: past a burst the server closes it with
// 1008 (policy violation).
func TestHandler_LimitsTheMessageRate(t *testing.T) {
	ts := newTestServer(t)
	conn, _, err := dial(t, ts.url+"/ws/room/K7M2P?token=good", testOrigin)
	require.NoError(t, err)
	readMessage(t, conn) // the snapshot

	for range messageBurst * 3 {
		if conn.Write(t.Context(), websocket.MessageText, []byte(`{"type":"confirm_reveal"}`)) != nil {
			break
		}
	}

	assert.Equal(t, websocket.StatusPolicyViolation, readCloseStatus(t, conn))
	assert.Less(t, len(ts.rooms.handledMessages()), messageBurst*3)
}

// blockingRooms never answers Connect before its context ends.
type blockingRooms struct{}

func (blockingRooms) Connect(ctx context.Context, _, _ string, _ room.Client) (Session, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// The handshake's authentication and room load have a deadline: after the upgrade, net/http no
// longer cancels the request context when the client goes away.
func TestHandler_ConnectHasADeadline(t *testing.T) {
	handler := NewHandler(blockingRooms{}, slog.New(slog.NewTextHandler(io.Discard, nil)), []string{testOrigin}, t.Context())
	handler.connectTimeout = 50 * time.Millisecond
	mux := http.NewServeMux()
	mux.Handle("GET /ws/room/{code}", handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	conn, _, err := dial(t, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws/room/K7M2P?token=good", testOrigin)
	require.NoError(t, err)
	assert.Equal(t, websocket.StatusInternalError, readCloseStatus(t, conn))
}
