package room

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vhrboliveira/impostor-game/internal/game"
	"github.com/vhrboliveira/impostor-game/internal/protocol"
)

// The web app lets every player of the round hold a button to see their role again at any
// moment. That is only safe because the server never sends the secret to whoever must not see
// it: these tests walk a whole round and inspect the encoded snapshots.
func TestSnapshot_TheSecretNeverReachesTheImpostorBeforeTheResult(t *testing.T) {
	tb := newTable(t)
	h := tb.harness
	// Duda joins over REST and connects after the round started: she sits it out.
	h.store.mu.Lock()
	h.store.playersInDB = append(h.store.playersInDB, PlayerState{ID: "duda", Name: "Duda", JoinedAt: h.clock.now})
	h.store.tokens[HashToken("t-duda")] = "duda"
	h.store.mu.Unlock()

	tb.selectTheme(1)
	tb.conns["duda"], tb.clients["duda"] = h.connect("t-duda")
	impostor := tb.impostor()
	insider := tb.others(impostor)[0]

	encoded := func(playerID string) string {
		data, err := json.Marshal(protocol.SnapshotMessage(tb.snapshot(playerID)))
		require.NoError(t, err)
		return string(data)
	}
	check := func(phase string) {
		t.Helper()
		require.Equal(t, phase, tb.snapshot("ana").Round.Phase)
		assert.Equal(t, "Pizza", *tb.snapshot(insider).Me.Secret, "%s: insiders keep the secret", phase)
		for _, id := range []string{impostor, "duda"} {
			assert.Nil(t, tb.snapshot(id).Me.Secret, "%s: %s has no secret", phase, id)
			if phase != "guess" { // the guess options include the secret on purpose
				assert.False(t, strings.Contains(encoded(id), "Pizza"), "%s: the secret leaked to %s", phase, id)
			}
		}
	}

	check("reveal")
	for _, id := range []string{"ana", "bia", "caio"} {
		tb.send(id, protocol.ClientMessage{Type: protocol.TypeConfirmReveal})
	}
	check("questions")
	for range 3 {
		tb.send(tb.currentTurn().AnswererID, protocol.ClientMessage{Type: protocol.TypeNextTurn})
	}
	check("discussion")
	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeStartVoting})
	check("voting")
	tb.everyoneVotesForTheImpostor()
	check("guess")

	tb.clock.Advance(game.GuessTimeout)
	tb.settle()
	s := tb.snapshot(impostor)
	require.Equal(t, "result", s.Round.Phase)
	assert.Equal(t, "Pizza", s.Round.Result.Secret, "the result reveals the secret to everyone")
}

// In the guess, only the found impostor receives the options: in a players theme they are every
// player of the round but the impostor, so anyone else would learn who the impostor is.
func TestSnapshot_GuessOptionsOnlyForTheImpostor(t *testing.T) {
	tb := newTable(t)
	tb.selectTheme(2)
	for _, id := range []string{"ana", "bia", "caio"} {
		tb.send(id, protocol.ClientMessage{Type: protocol.TypeConfirmReveal})
	}
	for range 3 {
		tb.send(tb.currentTurn().AnswererID, protocol.ClientMessage{Type: protocol.TypeNextTurn})
	}
	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeStartVoting})
	tb.everyoneVotesForTheImpostor()
	require.Equal(t, "guess", tb.snapshot("ana").Round.Phase)

	impostor := tb.impostor()
	assert.NotEmpty(t, tb.snapshot(impostor).Round.GuessOptions)
	for _, id := range tb.others(impostor) {
		assert.Empty(t, tb.snapshot(id).Round.GuessOptions, id)
	}
}

// During the reveal the confirmations are listed in player order, not in ring order: the ring
// (who asks whom) is only shown turn by turn.
func TestSnapshot_RevealConfirmationsInPlayerOrder(t *testing.T) {
	tb := newTable(t)
	tb.selectTheme(1)
	order := map[string]int{"ana": 0, "bia": 1, "caio": 2}
	var askers []string
	for _, turn := range tb.store.began[0].Turns {
		askers = append(askers, turn.AskerID)
	}
	// Pick two players whose ring order is the reverse of their player order.
	var first, second string
	for i := range askers {
		for j := i + 1; j < len(askers); j++ {
			if order[askers[i]] > order[askers[j]] {
				first, second = askers[j], askers[i]
			}
		}
	}
	require.NotEmpty(t, first, "the seeded ring must differ from the player order")

	tb.send(first, protocol.ClientMessage{Type: protocol.TypeConfirmReveal})
	tb.send(second, protocol.ClientMessage{Type: protocol.TypeConfirmReveal})
	assert.Equal(t, []string{first, second}, tb.snapshot("ana").Round.ConfirmedIDs)
}
