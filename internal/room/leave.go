package room

import (
	"errors"
	"log/slog"
	"time"

	"github.com/vhrboliveira/impostor-game/internal/protocol"
)

// DefaultOfflineRemoval is how long a player may stay offline before they are removed from the
// room. It is longer than the host grace period: phones drop the socket in the background.
const DefaultOfflineRemoval = 3 * time.Minute

// Reasons a player leaves the room, for the logs.
const (
	leftByThemselves = "left"
	removedByHost    = "removed by the host"
	removedOffline   = "offline too long"
)

// leaveRoom removes the player who asked to leave (their app asked for a confirmation first).
func (a *actor) leaveRoom(playerID string) {
	a.removePlayer(playerID, leftByThemselves)
}

// removeByHost lets the host remove another player of the room.
func (a *actor) removeByHost(playerID string, c Client, payload *protocol.RemovePlayerPayload) {
	if p := a.player(playerID); p == nil || !p.IsHost {
		c.Send(protocol.ErrorMessage(protocol.ErrCodeNotHost))
		return
	}
	target := a.player(payload.PlayerID)
	if target == nil || !target.Active() || target.ID == playerID {
		c.Send(protocol.ErrorMessage(protocol.ErrCodeInvalidPlayer))
		return
	}
	a.removePlayer(target.ID, removedByHost)
}

// removePlayer marks the player as gone, closes their connections with CloseRemoved and hands
// the host role on right away when they held it. They stay in the state (and the database) so the
// names of the round still resolve; an offline player of a round in progress already never holds
// it, and that is all a removed player is to the round.
func (a *actor) removePlayer(playerID, reason string) {
	p := a.player(playerID)
	if p == nil || !p.Active() {
		return
	}
	newHost := ""
	if p.IsHost {
		newHost = a.nextHost(playerID)
	}

	now := a.m.clock.Now().Truncate(time.Microsecond)
	ctx, cancel := storeContext()
	defer cancel()
	if err := a.m.store.RemovePlayer(ctx, a.state.RoomID, playerID, newHost, now); err != nil {
		a.logError("remove player", err)
		return
	}

	p.LeftAt = &now
	p.IsHost = false
	if newHost != "" {
		a.player(newHost).IsHost = true
		a.hostGrace.cancel()
		a.hostGraceOver = false
	}
	a.cancelOfflineRemoval(playerID)
	delete(a.confirmed, playerID)
	wasConnected := a.isConnected(playerID)
	for c := range a.conns[playerID] {
		c.Close(protocol.CloseRemoved, "removed from the room")
	}
	delete(a.conns, playerID) // their Leave finds nothing to do
	a.m.logger.Info("player removed", slog.String("room", a.state.Code),
		slog.String("player_id", playerID), slog.String("reason", reason))

	if wasConnected && len(a.conns) == 0 {
		if err := a.markEmpty(); errors.Is(err, ErrRoomNotFound) {
			a.stop() // the janitor deleted the room under this actor
			return
		}
		a.arm(&a.idle, a.m.cfg.IdleTimeout, a.stopIfIdle)
	}
	a.reviewHost()
	if !a.startQuestionsIfAllConfirmed() && !a.closeVotingIfAllVoted() {
		a.broadcast()
	}
}

// nextHost picks who takes over from a host who leaves: the oldest connected player, or the
// oldest one still in the room when nobody is connected. Empty when nobody is left.
func (a *actor) nextHost(leaving string) string {
	fallback := ""
	for _, p := range a.state.Players { // ordered by JoinedAt
		if p.ID == leaving || !p.Active() {
			continue
		}
		if a.isConnected(p.ID) {
			return p.ID
		}
		if fallback == "" {
			fallback = p.ID
		}
	}
	return fallback
}

// armOfflineRemoval starts the countdown that removes a player who stays offline.
func (a *actor) armOfflineRemoval(playerID string) {
	if a.offline == nil {
		a.offline = make(map[string]*timer)
	}
	t := a.offline[playerID]
	if t == nil {
		t = &timer{}
		a.offline[playerID] = t
	}
	a.arm(t, a.m.cfg.OfflineRemoval, func() {
		if !a.isConnected(playerID) {
			a.removePlayer(playerID, removedOffline)
		}
	})
}

func (a *actor) cancelOfflineRemoval(playerID string) {
	if t := a.offline[playerID]; t != nil {
		t.cancel()
		delete(a.offline, playerID)
	}
}

// armOfflineRemovals starts the countdown of every player still in the room who is offline and
// has none running: when the room loads (nobody knows since when they are away, so they get the
// full time) and when new players are read.
func (a *actor) armOfflineRemovals() {
	for _, p := range a.state.Players {
		if !p.Active() || a.isConnected(p.ID) {
			continue
		}
		if t := a.offline[p.ID]; t != nil && t.running() {
			continue
		}
		a.armOfflineRemoval(p.ID)
	}
}
