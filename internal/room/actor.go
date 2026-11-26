package room

import (
	"log/slog"
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

	// conns holds the open connections per player id. A player with several tabs or a
	// reconnect racing a stale socket has more than one.
	conns map[string]map[Client]struct{}

	idle      timer
	expiry    timer
	hostGrace timer
	// phaseTimer is the deadline of the round: the turn in progress or the end of the discussion.
	phaseTimer timer
	// hostGraceOver is set when the host has been offline for the whole grace period.
	hostGraceOver bool

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
		m:     m,
		state: state,
		cmds:  make(chan func()),
		done:  make(chan struct{}),
		conns: make(map[string]map[Client]struct{}),
		rng:   m.newRand(),
	}
}

// run processes commands until the actor stops.
func (a *actor) run() {
	a.arm(&a.expiry, a.state.ExpiresAt.Sub(a.m.clock.Now()), a.expire)
	// Normally cancelled right away by the first connection.
	a.arm(&a.idle, a.m.cfg.IdleTimeout, a.stopIfIdle)
	a.resumeRound()

	for !a.stopped {
		cmd := <-a.cmds
		cmd()
	}
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
	a.m.remove(a)
	close(a.done)
}

// connect attaches a client. It returns errActorStopped when the actor is gone.
func (a *actor) connect(playerID string, c Client) error {
	reply := make(chan error, 1)
	if !a.do(func() { reply <- a.handleConnect(playerID, c) }) {
		return errActorStopped
	}
	return <-reply
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

	wasEmpty := len(a.conns) == 0
	if a.conns[playerID] == nil {
		a.conns[playerID] = make(map[Client]struct{})
	}
	a.conns[playerID][c] = struct{}{}

	a.idle.cancel()
	if wasEmpty {
		a.markOccupied()
	}
	a.reviewHost()
	a.broadcast()
	return nil
}

func (a *actor) handleDisconnect(playerID string, c Client) {
	clients := a.conns[playerID]
	if _, ok := clients[c]; !ok {
		return
	}
	delete(clients, c)
	if len(clients) == 0 {
		delete(a.conns, playerID)
	}

	if len(a.conns) == 0 {
		a.markEmpty()
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

// markOccupied clears empty_since when the first player connects.
func (a *actor) markOccupied() {
	if a.state.EmptySince == nil {
		return
	}
	ctx, cancel := storeContext()
	defer cancel()
	if err := a.m.store.SetEmptySince(ctx, a.state.RoomID, nil); err != nil {
		a.logError("clear empty_since", err)
		return
	}
	a.state.EmptySince = nil
}

// markEmpty records when the last player left, so the janitor deletes the room after
// EMPTY_ROOM_TTL.
func (a *actor) markEmpty() {
	now := a.m.clock.Now()
	ctx, cancel := storeContext()
	defer cancel()
	if err := a.m.store.SetEmptySince(ctx, a.state.RoomID, &now); err != nil {
		a.logError("set empty_since", err)
		return
	}
	a.state.EmptySince = &now
}

func (a *actor) stopIfIdle() {
	if len(a.conns) == 0 {
		a.stop()
	}
}

// expire closes every connection with CloseRoomNotFound: the room reached its maximum lifetime
// and the janitor deletes it.
func (a *actor) expire() {
	for _, clients := range a.conns {
		for c := range clients {
			c.Close(protocol.CloseRoomNotFound, "room expired")
		}
	}
	a.conns = make(map[string]map[Client]struct{})
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
