package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/time/rate"

	"github.com/vhrboliveira/impostor-game/internal/protocol"
	"github.com/vhrboliveira/impostor-game/internal/room"
)

const (
	// idleTimeout closes a connection that sent nothing for this long. The web app pings every
	// 25 seconds, so only dead connections (a phone that lost the network) hit it.
	idleTimeout = 60 * time.Second
	// writeTimeout bounds each message written to a connection.
	writeTimeout = 10 * time.Second
	// outboundBuffer is how many messages other than snapshots (errors, pongs) may wait for a slow
	// connection before it is closed. Snapshots never queue up: the newest replaces the unsent one.
	outboundBuffer = 32
	// messageRate and messageBurst bound what one connection may send: the web app sends a ping
	// every 25 seconds and a command per tap. A connection over the limit is closed with 1008.
	messageRate  = rate.Limit(10)
	messageBurst = 30
	// defaultConnectTimeout bounds the authentication and room load of a handshake. After the
	// upgrade, net/http no longer cancels the request context when the client goes away.
	defaultConnectTimeout = 10 * time.Second
)

// Session is a player's connection inside a room. *room.Conn implements it.
type Session interface {
	Handle(msg protocol.ClientMessage)
	Leave()
}

// Connector authenticates a player and attaches a client to the room.
type Connector interface {
	Connect(ctx context.Context, code, token string, client room.Client) (Session, error)
}

// ManagerConnector adapts *room.Manager to Connector.
type ManagerConnector struct {
	Manager *room.Manager
}

// Connect implements Connector.
func (c ManagerConnector) Connect(ctx context.Context, code, token string, client room.Client) (Session, error) {
	conn, err := c.Manager.Connect(ctx, code, token, client)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// Handler serves GET /ws/room/{code}?token=...: it checks the origin, upgrades the connection,
// authenticates the player and then pumps messages between the socket and the room.
type Handler struct {
	rooms          Connector
	logger         *slog.Logger
	allowedOrigins map[string]struct{}
	shutdown       context.Context

	// conns tracks open connections: http.Server.Shutdown does not wait for hijacked ones.
	conns sync.WaitGroup

	connectTimeout time.Duration
}

// NewHandler creates the WebSocket handler. Connections are closed with 1001 (going away) when
// shutdown is cancelled, so clients reconnect to the next server process.
func NewHandler(rooms Connector, logger *slog.Logger, allowedOrigins []string, shutdown context.Context) *Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[normalizeOrigin(origin)] = struct{}{}
	}
	return &Handler{rooms: rooms, logger: logger, allowedOrigins: allowed, shutdown: shutdown, connectTimeout: defaultConnectTimeout}
}

func normalizeOrigin(origin string) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(origin)), "/")
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Browsers always send Origin on a WebSocket handshake. Checking it stops other sites from
	// opening sockets with a token they somehow obtained (cross-site WebSocket hijacking).
	if _, ok := h.allowedOrigins[normalizeOrigin(r.Header.Get("Origin"))]; !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"forbidden_origin"}` + "\n"))
		return
	}

	// The origin was checked above against ALLOWED_ORIGINS, which are full origins, so the
	// library's host-based check is not needed.
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		h.logger.DebugContext(r.Context(), "websocket upgrade failed", slog.Any("error", err))
		return
	}
	h.conns.Add(1)
	defer h.conns.Done()
	defer func() { _ = ws.CloseNow() }()
	ws.SetReadLimit(protocol.MaxMessageBytes)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	client := newClient()
	connectCtx, cancelConnect := context.WithTimeout(ctx, h.connectTimeout)
	session, err := h.rooms.Connect(connectCtx, r.PathValue("code"), r.URL.Query().Get("token"), client)
	cancelConnect()
	switch {
	case errors.Is(err, room.ErrRoomNotFound):
		_ = ws.Close(protocol.CloseRoomNotFound, "room not found")
		return
	case errors.Is(err, room.ErrUnauthorized):
		_ = ws.Close(protocol.CloseUnauthorized, "unauthorized")
		return
	case err != nil:
		h.logger.ErrorContext(r.Context(), "websocket connect failed", slog.Any("error", err))
		_ = ws.Close(websocket.StatusInternalError, "")
		return
	}
	defer session.Leave()

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		defer cancel() // stop the reader when the writer gives up
		h.writeLoop(ctx, ws, client)
	}()

	if h.readLoop(ctx, ws, client, session) {
		<-writerDone // let the write loop send the close frame the reader asked for
	}
	cancel()
	<-writerDone
}

// Wait blocks until every connection has ended (they close with 1001 once the shutdown
// context passed to NewHandler is cancelled) or ctx expires.
func (h *Handler) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		h.conns.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// readLoop decodes messages until the connection closes, stays idle for too long or sends faster
// than messageRate. Pings are answered here; everything else goes to the room. It reports whether
// it asked the write loop to close the connection.
func (h *Handler) readLoop(ctx context.Context, ws *websocket.Conn, c *client, session Session) bool {
	limiter := rate.NewLimiter(messageRate, messageBurst)
	for {
		readCtx, cancel := context.WithTimeout(ctx, idleTimeout)
		typ, data, err := ws.Read(readCtx)
		cancel()
		if err != nil {
			return false
		}
		if !limiter.Allow() {
			c.Close(protocol.ClosePolicyViolation, "too many messages")
			return true
		}

		if typ != websocket.MessageText {
			c.Send(protocol.ErrorMessage(protocol.ErrCodeInvalidMessage))
			continue
		}
		msg, err := protocol.ParseClientMessage(data)
		if err != nil {
			c.Send(protocol.ErrorMessage(protocol.ErrCodeInvalidMessage))
			continue
		}
		if msg.Type == protocol.TypePing {
			c.Send(protocol.PongMessage())
			continue
		}
		session.Handle(msg)
	}
}

// writeLoop writes queued messages and performs the close handshakes: the ones the room asks
// for (room expired, slow connection) and the one of a server shutdown.
func (h *Handler) writeLoop(ctx context.Context, ws *websocket.Conn, c *client) {
	for {
		select {
		case msg := <-c.out:
			if !h.write(ctx, ws, msg) {
				return
			}

		case <-c.snapshotReady:
			if msg, ok := c.takeSnapshot(); ok && !h.write(ctx, ws, msg) {
				return
			}

		case frame := <-c.closeReq:
			_ = ws.Close(websocket.StatusCode(frame.code), frame.reason)
			return

		case <-h.shutdown.Done():
			_ = ws.Close(websocket.StatusGoingAway, "server shutting down")
			return

		case <-ctx.Done():
			return
		}
	}
}

// write encodes and writes one message. It reports false when the connection is gone.
func (h *Handler) write(ctx context.Context, ws *websocket.Conn, msg protocol.ServerMessage) bool {
	data, err := json.Marshal(msg)
	if err != nil {
		h.logger.Error("encode websocket message", slog.String("type", msg.Type), slog.Any("error", err))
		return true
	}
	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return ws.Write(writeCtx, websocket.MessageText, data) == nil
}

type closeFrame struct {
	code   int
	reason string
}

// client is the room.Client of one connection. Its methods never block.
type client struct {
	out       chan protocol.ServerMessage
	closeReq  chan closeFrame
	closeOnce sync.Once

	// snapshot is the newest snapshot not written yet. A snapshot carries the whole room state, so
	// a newer one replaces it: bursts of room changes never fill the buffer of a healthy
	// connection. snapshotReady (capacity 1) wakes the write loop.
	mu            sync.Mutex
	snapshot      *protocol.ServerMessage
	snapshotReady chan struct{}
}

func newClient() *client {
	return &client{
		out:           make(chan protocol.ServerMessage, outboundBuffer),
		closeReq:      make(chan closeFrame, 1),
		snapshotReady: make(chan struct{}, 1),
	}
}

// Send queues a message. A snapshot replaces the unsent one. Other messages queue up; when the
// connection is too slow to drain them it is closed, and the player reconnects and receives a
// fresh snapshot.
func (c *client) Send(msg protocol.ServerMessage) {
	if msg.Type == protocol.TypeRoomSnapshot {
		c.mu.Lock()
		c.snapshot = &msg
		c.mu.Unlock()
		select {
		case c.snapshotReady <- struct{}{}:
		default: // the write loop is already woken
		}
		return
	}
	select {
	case c.out <- msg:
	default:
		c.Close(int(websocket.StatusTryAgainLater), "connection too slow")
	}
}

// takeSnapshot returns the pending snapshot, if any, and clears it.
func (c *client) takeSnapshot() (protocol.ServerMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snapshot == nil {
		return protocol.ServerMessage{}, false
	}
	msg := *c.snapshot
	c.snapshot = nil
	return msg, true
}

// Close asks the write loop to close the connection with the given code.
func (c *client) Close(code int, reason string) {
	c.closeOnce.Do(func() { c.closeReq <- closeFrame{code: code, reason: reason} })
}
