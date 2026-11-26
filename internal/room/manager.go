package room

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/vhrboliveira/impostor-game/internal/game"
	"github.com/vhrboliveira/impostor-game/internal/protocol"
)

const (
	// DefaultHostGracePeriod is how long a disconnected host keeps the role before the oldest
	// connected player takes over. Phones drop the socket whenever the app goes to the
	// background, so the transfer is not immediate.
	DefaultHostGracePeriod = 30 * time.Second
	// DefaultIdleTimeout is how long an actor stays in memory after its last connection closes.
	DefaultIdleTimeout = 2 * time.Minute

	// storeTimeout bounds each database call made by an actor.
	storeTimeout       = 5 * time.Second
	maxConnectAttempts = 3
)

// errActorStopped means the actor stopped between being looked up and receiving a command.
// Connect retries with a fresh actor.
var errActorStopped = errors.New("room actor stopped")

// ManagerConfig holds the timings of the room actors. Zero values use the defaults.
type ManagerConfig struct {
	HostGracePeriod    time.Duration
	IdleTimeout        time.Duration
	DiscussionDuration time.Duration
}

// Manager owns the active rooms: one actor goroutine per room with connected players. Actors
// are created on the first connection, loading the room from the database, and stop by
// themselves once the room has been idle for a while or expires.
type Manager struct {
	store  LiveStore
	logger *slog.Logger
	clock  Clock
	cfg    ManagerConfig
	// newRand gives each actor its own generator (*rand.Rand is not safe for concurrent use).
	newRand func() game.Rand

	mu     sync.Mutex
	actors map[string]*actor
}

// NewManager creates a Manager.
func NewManager(store LiveStore, logger *slog.Logger, cfg ManagerConfig) *Manager {
	if cfg.HostGracePeriod <= 0 {
		cfg.HostGracePeriod = DefaultHostGracePeriod
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = DefaultIdleTimeout
	}
	if cfg.DiscussionDuration <= 0 {
		cfg.DiscussionDuration = game.DiscussionDuration
	}
	return &Manager{
		store:  store,
		logger: logger,
		clock:  systemClock{},
		cfg:    cfg,
		newRand: func() game.Rand {
			// The top-level functions of math/rand/v2 are randomly seeded.
			return rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
		},
		actors: make(map[string]*actor),
	}
}

// Conn is one connection of a player to an active room.
type Conn struct {
	actor    *actor
	playerID string
	client   Client
}

// PlayerID returns the id of the connected player.
func (s *Conn) PlayerID() string { return s.playerID }

// Handle passes a message from the player to the room. Commands are processed in order; the
// answer (a new snapshot or an error) arrives through the Client.
func (s *Conn) Handle(msg protocol.ClientMessage) {
	s.actor.do(func() { s.actor.handleMessage(s.playerID, s.client, msg) })
}

// Leave tells the room that this connection closed.
func (s *Conn) Leave() {
	s.actor.do(func() { s.actor.handleDisconnect(s.playerID, s.client) })
}

// Connect authenticates a player by the session token received when they created or joined the
// room and attaches the client to the room's actor, which immediately sends it a snapshot.
// It returns ErrRoomNotFound (unknown, malformed or expired code) or ErrUnauthorized.
func (m *Manager) Connect(ctx context.Context, rawCode, token string, client Client) (*Conn, error) {
	code, ok := NormalizeCode(rawCode)
	if !ok {
		return nil, ErrRoomNotFound
	}
	if token == "" {
		return nil, ErrUnauthorized
	}

	playerID, err := m.store.Authenticate(ctx, code, HashToken(token), m.clock.Now())
	if err != nil {
		return nil, err
	}

	for range maxConnectAttempts {
		a, err := m.actorFor(ctx, code)
		if err != nil {
			return nil, err
		}

		err = a.connect(playerID, client)
		if errors.Is(err, errActorStopped) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return &Conn{actor: a, playerID: playerID, client: client}, nil
	}
	return nil, errActorStopped
}

// actorFor returns the running actor of the room, loading the room and starting one if needed.
func (m *Manager) actorFor(ctx context.Context, code string) (*actor, error) {
	m.mu.Lock()
	a, ok := m.actors[code]
	m.mu.Unlock()
	if ok {
		return a, nil
	}

	// Loading happens outside the lock so a slow query does not block other rooms.
	state, err := m.store.LoadRoom(ctx, code, m.clock.Now())
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.actors[code]; ok {
		return a, nil // another connection started it meanwhile
	}
	a = newActor(m, state)
	m.actors[code] = a
	go a.run()
	return a, nil
}

// remove forgets a stopping actor, unless it was already replaced.
func (m *Manager) remove(a *actor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.actors[a.state.Code] == a {
		delete(m.actors, a.state.Code)
	}
}

// storeContext bounds one database call made by an actor.
func storeContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), storeTimeout)
}
