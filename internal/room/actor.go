package room

import (
	"errors"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/vhrboliveira/impostor-game/internal/game"
	"github.com/vhrboliveira/impostor-game/internal/protocol"
)

// actor owns the live state of one room. Every change runs as a command on its goroutine, one
// at a time, so the state needs no locks. Commands must not block for long: database calls are
// bounded by storeTimeout and clients never block.
type actor struct {
	m     *Manager
	state RoomState

	cmds    chan func()
	done    chan struct{}
	stopped bool

	// conns holds the open connections per player id, each with its connection order. A player
	// with several tabs or a reconnect racing a stale socket has more than one (at most
	// maxConnsPerPlayer).
	conns   map[string]map[Client]uint64
	connSeq uint64

	idle      timer
	expiry    timer
	hostGrace timer
	// phaseTimer is the deadline of the round: the turn in progress or the end of the discussion.
	phaseTimer timer
	// hostGraceOver is set when the host has been offline for the whole grace period.
	hostGraceOver bool
	// offline holds the countdown of each player offline, after which they are removed.
	offline map[string]*timer

	// marked holds the players whose first connection this actor already recorded.
	marked map[string]bool

	// confirmed holds who tapped "Entendi" during the reveal. It lives in memory only: after a
	// reload the players confirm again.
	confirmed map[string]bool

	rng game.Rand
}

// timer is a pending clock callback. gen invalidates callbacks that were already on their way
// when the timer was cancelled or re-armed.
type timer struct {
	gen  int
	stop func() bool
}

func (t *timer) running() bool { return t.stop != nil }

func (t *timer) cancel() {
	t.gen++
	if t.stop != nil {
		t.stop()
		t.stop = nil
	}
}

func newActor(m *Manager, state RoomState) *actor {
	return &actor{
		m:      m,
		state:  state,
		cmds:   make(chan func()),
		done:   make(chan struct{}),
		conns:  make(map[string]map[Client]uint64),
		marked: make(map[string]bool),
		rng:    m.newRand(),
	}
}

// run processes commands until the actor stops.
func (a *actor) run() {
	a.arm(&a.expiry, a.state.ExpiresAt.Sub(a.m.clock.Now()), a.expire)
	// Normally cancelled right away by the first connection.
	a.arm(&a.idle, a.m.cfg.IdleTimeout, a.stopIfIdle)
	a.exec(a.resumeRound)
	a.exec(a.armOfflineRemovals)

	for !a.stopped {
		a.exec(<-a.cmds)
	}
}

// exec runs one command. A panic stops this room only: it is logged with its stack, the room's
// connections are closed with CloseInternalError (the clients reconnect, which loads the room
// again from the database) and the actor stops, so the process and the other rooms keep running.
func (a *actor) exec(cmd func()) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		a.m.logger.Error("room actor panic", slog.String("room", a.state.Code),
			slog.Any("panic", r), slog.String("stack", string(debug.Stack())))
		for _, clients := range a.conns {
			for c := range clients {
				c.Close(protocol.CloseInternalError, "internal error")
			}
		}
		a.conns = make(map[string]map[Client]uint64)
		if !a.stopped {
			a.stop()
		}
	}()
	cmd()
}

// do queues a command. It returns false when the actor has stopped.
func (a *actor) do(cmd func()) bool {
	select {
	case a.cmds <- cmd:
		return true
	case <-a.done:
		return false
	}
}

// arm schedules fn on the actor after d, replacing whatever t had scheduled.
func (a *actor) arm(t *timer, d time.Duration, fn func()) {
	t.cancel()
	gen := t.gen
	t.stop = a.m.clock.AfterFunc(max(d, 0), func() {
		a.do(func() {
			if t.gen != gen {
				return // cancelled or re-armed after this callback started
			}
			t.stop = nil
			fn()
		})
	})
}

func (a *actor) stop() {
	a.stopped = true
	a.idle.cancel()
	a.expiry.cancel()
	a.hostGrace.cancel()
	a.phaseTimer.cancel()
	for _, t := range a.offline {
		t.cancel()
	}
	a.m.remove(a)
	close(a.done)
}

// connect attaches a client. It returns errActorStopped when the actor is gone.
func (a *actor) connect(playerID string, c Client) error {
	return a.call(func() error { return a.handleConnect(playerID, c) })
}

// call runs fn on the actor and waits for its result. It returns errActorStopped when the actor
// is gone, including when fn panicked and stopped it before replying.
func (a *actor) call(fn func() error) error {
	reply := make(chan error, 1)
	if !a.do(func() { reply <- fn() }) {
		return errActorStopped
	}
	select {
	case err := <-reply:
		return err
	case <-a.done:
		select {
		case err := <-reply:
			return err
		default:
			return errActorStopped
		}
	}
}

func (a *actor) handleConnect(playerID string, c Client) error {
	if a.player(playerID) == nil {
		// Joined over REST after this actor loaded the room.
		if err := a.reloadPlayers(); err != nil {
			return err
		}
		if a.player(playerID) == nil {
			return ErrUnauthorized
		}
	}
	if !a.player(playerID).Active() {
		return ErrUnauthorized // removed between the token check and this connection
	}
	a.cancelOfflineRemoval(playerID)

	if a.conns[playerID] == nil {
		a.conns[playerID] = make(map[Client]uint64)
	}
	if len(a.conns[playerID]) >= maxConnsPerPlayer {
		a.dropOldestConn(playerID)
	}
	a.connSeq++
	a.conns[playerID][c] = a.connSeq

	a.idle.cancel()
	if !a.marked[playerID] {
		ctx, cancel := storeContext()
		if err := a.m.store.MarkConnected(ctx, playerID, a.m.clock.Now()); err != nil {
			a.logError("mark player connected", err) // retried on the next connection
		} else {
			a.marked[playerID] = true
		}
		cancel()
	}
	// On every connection, not only the first: a clear that failed earlier is retried (it is a
	// no-op once the marker is clear).
	if err := a.markOccupied(); errors.Is(err, ErrRoomNotFound) {
		a.expire() // the janitor deleted the room under this actor
		return nil
	}
	a.reviewHost()
	a.broadcast()
	return nil
}

// dropOldestConn closes the player's oldest connection to make room for a new one. It is removed
// right away; its Leave finds nothing to do.
func (a *actor) dropOldestConn(playerID string) {
	var (
		oldest Client
		seq    uint64
	)
	for c, s := range a.conns[playerID] {
		if oldest == nil || s < seq {
			oldest, seq = c, s
		}
	}
	delete(a.conns[playerID], oldest)
	oldest.Close(protocol.ClosePolicyViolation, "too many connections")
}

func (a *actor) handleDisconnect(playerID string, c Client) {
	clients := a.conns[playerID]
	if _, ok := clients[c]; !ok {
		return
	}
	delete(clients, c)
	if len(clients) == 0 {
		delete(a.conns, playerID)
		a.armOfflineRemoval(playerID)
	}

	if len(a.conns) == 0 {
		if err := a.markEmpty(); errors.Is(err, ErrRoomNotFound) {
			a.stop() // the janitor deleted the room under this actor
			return
		}
		a.arm(&a.idle, a.m.cfg.IdleTimeout, a.stopIfIdle)
	}
	a.reviewHost()
	// An offline player no longer holds the reveal or the voting.
	if !a.startQuestionsIfAllConfirmed() && !a.closeVotingIfAllVoted() {
		a.broadcast()
	}
}

func (a *actor) handleMessage(playerID string, c Client, msg protocol.ClientMessage) {
	switch msg.Type {
	case protocol.TypeStartMatch:
		a.startMatch(playerID, c)
	case protocol.TypeSelectTheme:
		a.selectTheme(playerID, c, msg.SelectTheme)
	case protocol.TypeConfirmReveal:
		a.confirmReveal(playerID, c)
	case protocol.TypeStartQuestions:
		a.forceStartQuestions(playerID, c)
	case protocol.TypeNextTurn:
		a.nextTurn(playerID, c)
	case protocol.TypeExtendTurn:
		a.extendTurn(playerID, c)
	case protocol.TypeExtendDiscussion:
		a.extendDiscussion(playerID, c)
	case protocol.TypeStartVoting:
		a.endDiscussion(playerID, c)
	case protocol.TypeVote:
		a.vote(playerID, c, msg.Vote)
	case protocol.TypeGuess:
		a.guess(playerID, c, msg.Guess)
	case protocol.TypeNextRound:
		a.nextRound(playerID, c)
	case protocol.TypeEndMatch:
		a.endMatch(playerID, c)
	case protocol.TypePlayAgain:
		a.playAgain(playerID, c, msg.PlayAgain)
	case protocol.TypeLeaveRoom:
		a.leaveRoom(playerID)
	case protocol.TypeRemovePlayer:
		a.removeByHost(playerID, c, msg.Remove)
	default:
		// ping is answered by the transport; nothing else reaches here.
		c.Send(protocol.ErrorMessage(protocol.ErrCodeInvalidMessage))
	}
}

// startMatch moves the lobby to the first round. Only the host can start, with at least
// game.MinPlayers players connected.
func (a *actor) startMatch(playerID string, c Client) {
	if p := a.player(playerID); p == nil || !p.IsHost {
		c.Send(protocol.ErrorMessage(protocol.ErrCodeNotHost))
		return
	}
	if a.state.Match.Status != game.MatchLobby {
		c.Send(protocol.ErrorMessage(protocol.ErrCodeInvalidPhase))
		return
	}
	if len(a.conns) < game.MinPlayers {
		c.Send(protocol.ErrorMessage(protocol.ErrCodeNotEnoughPlayers))
		return
	}

	ctx, cancel := storeContext()
	defer cancel()
	started, err := a.m.store.StartMatch(ctx, StartMatchParams{
		RoomID:  a.state.RoomID,
		MatchID: a.state.Match.ID,
		Now:     a.m.clock.Now(),
	})
	if err != nil {
		a.logError("start match", err)
		c.Send(protocol.ErrorMessage(protocol.ErrCodeInternal))
		return
	}

	a.state.Match.Status = game.MatchInProgress
	round := started.Round
	a.state.Round = &round
	a.state.Players = started.Players
	a.broadcast()
}

// reviewHost hands the host role to the oldest connected player once the host has been offline
// for the whole grace period. It runs after every presence change.
func (a *actor) reviewHost() {
	host := a.host()
	switch {
	case host != nil && a.isConnected(host.ID):
		a.hostGrace.cancel()
		a.hostGraceOver = false
	case len(a.conns) == 0:
		// Nobody to hand the role to. Whoever comes back first starts a new grace period.
		a.hostGrace.cancel()
		a.hostGraceOver = false
	case a.hostGraceOver:
		a.transferHost()
	case !a.hostGrace.running():
		a.arm(&a.hostGrace, a.m.cfg.HostGracePeriod, func() {
			a.hostGraceOver = true
			a.reviewHost()
			a.broadcast()
		})
	}
}

func (a *actor) transferHost() {
	var next *PlayerState
	for i := range a.state.Players { // ordered by JoinedAt: the first connected is the oldest
		p := &a.state.Players[i]
		if !p.IsHost && a.isConnected(p.ID) {
			next = p
			break
		}
	}
	if next == nil {
		return
	}

	ctx, cancel := storeContext()
	defer cancel()
	if err := a.m.store.TransferHost(ctx, a.state.RoomID, next.ID); err != nil {
		a.logError("transfer host", err)
		return
	}

	for i := range a.state.Players {
		a.state.Players[i].IsHost = a.state.Players[i].ID == next.ID
	}
	a.hostGrace.cancel()
	a.hostGraceOver = false
	a.m.logger.Info("host transferred", slog.String("room", a.state.Code), slog.String("player_id", next.ID))
}

// markOccupied clears empty_since while someone is connected. A failure leaves the marker set
// (the next connection retries); ErrRoomNotFound means the room is gone.
func (a *actor) markOccupied() error {
	if a.state.EmptySince == nil {
		return nil
	}
	ctx, cancel := storeContext()
	defer cancel()
	if err := a.m.store.SetEmptySince(ctx, a.state.RoomID, nil); err != nil {
		a.logError("clear empty_since", err)
		return err
	}
	a.state.EmptySince = nil
	return nil
}

// markEmpty records when the last player left, so the janitor deletes the room after
// EMPTY_ROOM_TTL. A failure leaves state.EmptySince nil, so stopIfIdle retries it.
func (a *actor) markEmpty() error {
	now := a.m.clock.Now()
	ctx, cancel := storeContext()
	defer cancel()
	if err := a.m.store.SetEmptySince(ctx, a.state.RoomID, &now); err != nil {
		a.logError("set empty_since", err)
		return err
	}
	a.state.EmptySince = &now
	return nil
}

// stopIfIdle stops the actor once nobody is connected. It never leaves the room marked occupied:
// an actor started only by a REST call (after a restart, the stored marker may be stale) or whose
// last markEmpty failed writes the marker first, and waits another idle period if that fails.
func (a *actor) stopIfIdle() {
	if len(a.conns) > 0 {
		return
	}
	if a.state.EmptySince == nil {
		err := a.markEmpty()
		if err != nil && !errors.Is(err, ErrRoomNotFound) {
			a.arm(&a.idle, a.m.cfg.IdleTimeout, a.stopIfIdle)
			return
		}
	}
	a.stop()
}

// expire closes every connection with CloseRoomNotFound: the room reached its maximum lifetime
// and the janitor deletes it.
func (a *actor) expire() {
	for _, clients := range a.conns {
		for c := range clients {
			c.Close(protocol.CloseRoomNotFound, "room expired")
		}
	}
	a.conns = make(map[string]map[Client]uint64)
	a.stop()
}

func (a *actor) reloadPlayers() error {
	ctx, cancel := storeContext()
	defer cancel()
	players, err := a.m.store.LoadPlayers(ctx, a.state.RoomID, a.state.Match.ID)
	if err != nil {
		return err
	}
	a.state.Players = players
	a.armOfflineRemovals()
	return nil
}

// broadcast sends every connected player their own snapshot.
func (a *actor) broadcast() {
	for playerID, clients := range a.conns {
		msg := protocol.SnapshotMessage(a.snapshotFor(playerID))
		for c := range clients {
			c.Send(msg)
		}
	}
}

func (a *actor) player(id string) *PlayerState {
	for i := range a.state.Players {
		if a.state.Players[i].ID == id {
			return &a.state.Players[i]
		}
	}
	return nil
}

func (a *actor) host() *PlayerState {
	for i := range a.state.Players {
		if a.state.Players[i].IsHost {
			return &a.state.Players[i]
		}
	}
	return nil
}

func (a *actor) isConnected(playerID string) bool {
	return len(a.conns[playerID]) > 0
}

func (a *actor) logError(action string, err error) {
	a.m.logger.Error("room actor: "+action, slog.String("room", a.state.Code), slog.Any("error", err))
}
