package room

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vhrboliveira/impostor-game/internal/game"
	"github.com/vhrboliveira/impostor-game/internal/protocol"
)

// table is a started match with Ana (host), Bia and Caio connected, in theme selection.
type table struct {
	*harness
	conns   map[string]*Conn
	clients map[string]*fakeClient
}

func newTable(t *testing.T) *table {
	t.Helper()
	return newTableWith(t, nil)
}

// newTableWith lets the test change the stored room before it is loaded.
func newTableWith(t *testing.T, setup func(*fakeLiveStore)) *table {
	t.Helper()
	tb := &table{harness: newHarness(t), conns: map[string]*Conn{}, clients: map[string]*fakeClient{}}
	if setup != nil {
		setup(tb.store)
	}
	for _, id := range []string{"ana", "bia", "caio"} {
		tb.conns[id], tb.clients[id] = tb.connect("t-" + id)
	}
	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeStartMatch})
	require.Equal(t, "theme_selection", tb.snapshot("ana").Round.Phase)
	return tb
}

func (tb *table) send(playerID string, msg protocol.ClientMessage) {
	tb.conns[playerID].Handle(msg)
	tb.settle()
}

func (tb *table) snapshot(playerID string) protocol.RoomSnapshot {
	return tb.clients[playerID].lastSnapshot(tb.t)
}

func (tb *table) selectTheme(themeID int64) {
	tb.send("ana", protocol.ClientMessage{
		Type:        protocol.TypeSelectTheme,
		SelectTheme: &protocol.SelectThemePayload{ThemeID: themeID},
	})
}

// toQuestions picks the words theme and has everyone confirm the reveal.
func (tb *table) toQuestions() {
	tb.selectTheme(1)
	for _, id := range []string{"ana", "bia", "caio"} {
		tb.send(id, protocol.ClientMessage{Type: protocol.TypeConfirmReveal})
	}
	require.Equal(tb.t, "questions", tb.snapshot("ana").Round.Phase)
}

func (tb *table) currentTurn() protocol.TurnView {
	turns := tb.snapshot("ana").Round.Turns
	require.NotEmpty(tb.t, turns)
	return turns[len(turns)-1]
}

func TestSelectTheme_Errors(t *testing.T) {
	t.Run("only the host picks", func(t *testing.T) {
		tb := newTable(t)
		tb.send("bia", protocol.ClientMessage{Type: protocol.TypeSelectTheme, SelectTheme: &protocol.SelectThemePayload{ThemeID: 1}})

		assert.Equal(t, protocol.ErrCodeNotHost, tb.clients["bia"].lastError())
	})

	t.Run("not in the lobby", func(t *testing.T) {
		h := newHarness(t)
		ana, client := h.connect("t-ana")
		ana.Handle(protocol.ClientMessage{Type: protocol.TypeSelectTheme, SelectTheme: &protocol.SelectThemePayload{ThemeID: 1}})
		h.settle()

		assert.Equal(t, protocol.ErrCodeInvalidPhase, client.lastError())
	})

	t.Run("unknown theme", func(t *testing.T) {
		tb := newTable(t)
		tb.selectTheme(99)

		assert.Equal(t, protocol.ErrCodeThemeUnavailable, tb.clients["ana"].lastError())
		assert.Empty(t, tb.store.began)
	})

	t.Run("a words theme without unused words", func(t *testing.T) {
		tb := newTable(t)
		tb.selectTheme(3)

		assert.Equal(t, protocol.ErrCodeThemeUnavailable, tb.clients["ana"].lastError())
		assert.Equal(t, "theme_selection", tb.snapshot("ana").Round.Phase)
	})

	t.Run("fewer questions than players", func(t *testing.T) {
		tb := newTable(t)
		tb.store.candidates[1] = RoundCandidates{Words: wordList(11, "Pizza", "Sushi", "Lasanha", "Pastel"), Questions: questionList("q", 2)}
		tb.selectTheme(1)

		assert.Equal(t, protocol.ErrCodeThemeUnavailable, tb.clients["ana"].lastError())
	})

	t.Run("needs three connected players", func(t *testing.T) {
		tb := newTable(t)
		tb.conns["caio"].Leave()
		tb.settle()
		tb.selectTheme(1)

		assert.Equal(t, protocol.ErrCodeNotEnoughPlayers, tb.clients["ana"].lastError())
	})
}

func TestSelectTheme_StartsTheRevealWithOneImpostor(t *testing.T) {
	tb := newTable(t)

	tb.selectTheme(1)

	require.Len(t, tb.store.began, 1)
	plan := tb.store.began[0]
	assert.Equal(t, int64(1), plan.ThemeID)
	assert.False(t, plan.ThemeDrawn)
	assert.Equal(t, int64(11), plan.WordID)
	require.Len(t, plan.Turns, 3)

	askers, answerers, questions := map[string]bool{}, map[string]bool{}, map[int64]bool{}
	for i, turn := range plan.Turns {
		askers[turn.AskerID] = true
		answerers[turn.AnswererID] = true
		questions[turn.QuestionID] = true
		assert.Equal(t, plan.Turns[(i+1)%3].AskerID, turn.AnswererID, "whoever answers asks next")
	}
	assert.Len(t, askers, 3)
	assert.Len(t, answerers, 3)
	assert.Len(t, questions, 3, "questions never repeat within a round")

	impostors := 0
	for _, id := range []string{"ana", "bia", "caio"} {
		s := tb.snapshot(id)
		require.Equal(t, "reveal", s.Round.Phase)
		assert.Equal(t, "Comidas", *s.Round.ThemeName)
		assert.Equal(t, 3, s.Round.TotalTurns)
		assert.Empty(t, s.Round.Turns, "no question before the reveal ends")
		assert.Empty(t, s.Round.ThemeOptions)
		require.NotNil(t, s.Me.Role)
		if *s.Me.Role == protocol.RoleImpostor {
			impostors++
			assert.Equal(t, plan.ImpostorID, id)
			assert.Nil(t, s.Me.Secret, "the impostor never receives the secret")
		} else {
			require.NotNil(t, s.Me.Secret)
			assert.Equal(t, "Pizza", *s.Me.Secret)
		}
	}
	assert.Equal(t, 1, impostors)
}

func TestSelectTheme_RandomSkipsUnavailableThemes(t *testing.T) {
	for range 5 { // the shuffle order depends on the seed; every order must end on a usable theme
		tb := newTable(t)
		tb.send("ana", protocol.ClientMessage{Type: protocol.TypeSelectTheme, SelectTheme: &protocol.SelectThemePayload{Random: true}})

		require.Len(t, tb.store.began, 1)
		assert.NotEqual(t, int64(3), tb.store.began[0].ThemeID)
		assert.True(t, tb.store.began[0].ThemeDrawn)
	}
}

func TestSelectTheme_PlayersThemeUsesASubjectWhoIsNotTheImpostor(t *testing.T) {
	tb := newTable(t)

	tb.selectTheme(2)

	plan := tb.store.began[0]
	require.NotEmpty(t, plan.SubjectID)
	assert.NotEqual(t, plan.ImpostorID, plan.SubjectID)
	assert.Zero(t, plan.WordID)
	subjectName := tb.store.state.Players[0].Name
	for _, p := range tb.store.state.Players {
		if p.ID == plan.SubjectID {
			subjectName = p.Name
		}
	}
	for _, id := range []string{"ana", "bia", "caio"} {
		if id != plan.ImpostorID {
			assert.Equal(t, subjectName, *tb.snapshot(id).Me.Secret)
		}
	}
}

func TestSelectTheme_OfflinePlayersSitTheRoundOut(t *testing.T) {
	tb := newTable(t)
	h := tb.harness
	h.store.mu.Lock()
	h.store.playersInDB = append(h.store.playersInDB, PlayerState{ID: "duda", Name: "Duda", JoinedAt: h.clock.now})
	h.store.tokens[HashToken("t-duda")] = "duda"
	h.store.mu.Unlock()
	duda, dudaClient := h.connect("t-duda")
	duda.Leave()
	h.settle()

	tb.selectTheme(1)

	for _, turn := range tb.store.began[0].Turns {
		assert.NotEqual(t, "duda", turn.AskerID)
		assert.NotEqual(t, "duda", turn.AnswererID)
	}
	tb.conns["duda"], tb.clients["duda"] = h.connect("t-duda")
	me := tb.snapshot("duda").Me
	assert.Nil(t, me.Role, "Duda was offline when the round started")
	assert.Nil(t, me.Secret)
	_ = dudaClient

	tb.send("duda", protocol.ClientMessage{Type: protocol.TypeConfirmReveal})
	assert.Equal(t, protocol.ErrCodeNotParticipant, tb.clients["duda"].lastError())
}

func TestReveal_QuestionsStartWhenEveryConnectedPlayerConfirmed(t *testing.T) {
	tb := newTable(t)
	tb.selectTheme(1)

	tb.send("bia", protocol.ClientMessage{Type: protocol.TypeConfirmReveal})
	tb.send("bia", protocol.ClientMessage{Type: protocol.TypeConfirmReveal}) // twice is harmless
	assert.Equal(t, []string{"bia"}, tb.snapshot("ana").Round.ConfirmedIDs)

	tb.send("caio", protocol.ClientMessage{Type: protocol.TypeConfirmReveal})
	assert.Equal(t, "reveal", tb.snapshot("ana").Round.Phase)
	assert.Len(t, tb.snapshot("ana").Round.ConfirmedIDs, 2)

	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeConfirmReveal})

	s := tb.snapshot("bia")
	assert.Equal(t, "questions", s.Round.Phase)
	assert.Empty(t, s.Round.ConfirmedIDs)
	require.Len(t, s.Round.Turns, 1, "only the first turn is visible")
	turn := s.Round.Turns[0]
	assert.Equal(t, 0, turn.Position)
	assert.NotEmpty(t, turn.Question)
	require.NotNil(t, turn.DeadlineAt)
	assert.Equal(t, tb.clock.Now().Add(180*time.Second), *turn.DeadlineAt)
	assert.Equal(t, []string{"turn-0@12:03:00"}, tb.store.questions)
}

func TestReveal_TheHostCanStartWithoutEveryone(t *testing.T) {
	tb := newTable(t)
	tb.selectTheme(1)

	tb.send("bia", protocol.ClientMessage{Type: protocol.TypeStartQuestions})
	assert.Equal(t, protocol.ErrCodeNotHost, tb.clients["bia"].lastError())

	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeStartQuestions})
	assert.Equal(t, "questions", tb.snapshot("ana").Round.Phase)
}

func TestReveal_AnOfflinePlayerDoesNotHoldTheRound(t *testing.T) {
	tb := newTable(t)
	tb.selectTheme(1)
	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeConfirmReveal})
	tb.send("bia", protocol.ClientMessage{Type: protocol.TypeConfirmReveal})

	tb.conns["caio"].Leave()
	tb.settle()

	assert.Equal(t, "questions", tb.snapshot("ana").Round.Phase)
}

func TestQuestions_TheAnswererAdvances(t *testing.T) {
	tb := newTable(t)
	tb.toQuestions()
	first := tb.currentTurn()
	other := "ana"
	for _, id := range []string{"ana", "bia", "caio"} {
		if id != first.AnswererID && id != "ana" {
			other = id
		}
	}

	tb.send(other, protocol.ClientMessage{Type: protocol.TypeNextTurn})
	assert.Equal(t, protocol.ErrCodeNotYourTurn, tb.clients[other].lastError())

	tb.clock.Advance(20 * time.Second)
	tb.send(first.AnswererID, protocol.ClientMessage{Type: protocol.TypeNextTurn})

	turns := tb.snapshot("bia").Round.Turns
	require.Len(t, turns, 2)
	assert.Nil(t, turns[0].DeadlineAt, "past turns carry no deadline")
	assert.Equal(t, first.AnswererID, turns[1].AskerID, "the answerer asks next")
	assert.Equal(t, tb.clock.Now().Add(180*time.Second), *turns[1].DeadlineAt)
	require.Len(t, tb.store.advanced, 1)
	assert.Equal(t, endedByAnswerer, tb.store.advanced[0].EndedBy)
	assert.Equal(t, "turn-1", tb.store.advanced[0].NextTurnID)
}

func TestQuestions_TheHostCanAdvanceAnyTurn(t *testing.T) {
	tb := newTable(t)
	tb.toQuestions()
	for tb.currentTurn().AnswererID == "ana" {
		tb.send("ana", protocol.ClientMessage{Type: protocol.TypeNextTurn})
	}
	before := len(tb.store.advanced)

	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeNextTurn})

	require.Len(t, tb.store.advanced, before+1)
	assert.Equal(t, endedByHost, tb.store.advanced[before].EndedBy)
}

func TestQuestions_ExtendAndTimeout(t *testing.T) {
	tb := newTable(t)
	tb.toQuestions()
	first := tb.currentTurn()

	tb.send(first.AnswererID, protocol.ClientMessage{Type: protocol.TypeExtendTurn})
	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeExtendTurn})
	assert.Equal(t, first.DeadlineAt.Add(2*time.Minute), *tb.currentTurn().DeadlineAt)
	assert.Len(t, tb.store.extended, 2)

	tb.clock.Advance(4 * time.Minute)
	tb.settle()
	assert.Len(t, tb.snapshot("ana").Round.Turns, 1, "the extended turn is still running")

	tb.clock.Advance(time.Minute)
	tb.settle()
	assert.Len(t, tb.snapshot("ana").Round.Turns, 2, "the turn advanced by itself when time ran out")
	assert.Equal(t, endedByTimeout, tb.store.advanced[0].EndedBy)
}

func TestQuestions_TheLastTurnOpensTheDiscussionThenTheVoting(t *testing.T) {
	tb := newTable(t)
	tb.toQuestions()
	for range 3 {
		tb.send(tb.currentTurn().AnswererID, protocol.ClientMessage{Type: protocol.TypeNextTurn})
	}

	s := tb.snapshot("caio")
	assert.Equal(t, "discussion", s.Round.Phase)
	assert.Len(t, s.Round.Turns, 3, "the whole ring is visible during the discussion")
	for _, turn := range s.Round.Turns {
		assert.Nil(t, turn.DeadlineAt)
	}
	require.NotNil(t, s.Round.PhaseDeadlineAt)
	assert.Equal(t, tb.clock.Now().Add(3*time.Minute), *s.Round.PhaseDeadlineAt)
	last := tb.store.advanced[len(tb.store.advanced)-1]
	assert.Empty(t, last.NextTurnID)

	tb.send("caio", protocol.ClientMessage{Type: protocol.TypeNextTurn})
	assert.Equal(t, protocol.ErrCodeInvalidPhase, tb.clients["caio"].lastError())

	tb.clock.Advance(3 * time.Minute)
	tb.settle()

	s = tb.snapshot("bia")
	assert.Equal(t, "voting", s.Round.Phase)
	require.NotNil(t, s.Round.PhaseDeadlineAt)
	assert.Equal(t, tb.clock.Now().Add(game.VoteTimeout), *s.Round.PhaseDeadlineAt)
	assert.Len(t, s.Round.Turns, 3)
	assert.Equal(t, 1, tb.store.votings)
}

func TestDiscussion_TheHostCanEndItEarly(t *testing.T) {
	tb := newTable(t)
	tb.toQuestions()
	for range 3 {
		tb.send(tb.currentTurn().AnswererID, protocol.ClientMessage{Type: protocol.TypeNextTurn})
	}

	tb.send("bia", protocol.ClientMessage{Type: protocol.TypeStartVoting})
	assert.Equal(t, protocol.ErrCodeNotHost, tb.clients["bia"].lastError())

	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeStartVoting})
	assert.Equal(t, "voting", tb.snapshot("ana").Round.Phase)

	tb.clock.Advance(3 * time.Minute)
	tb.settle()
	assert.Equal(t, 1, tb.store.votings, "the cancelled timer does not start the voting again")
}

func TestDiscussion_TheHostCanAddAMinute(t *testing.T) {
	tb := newTable(t)
	tb.toQuestions()
	for range 3 {
		tb.send(tb.currentTurn().AnswererID, protocol.ClientMessage{Type: protocol.TypeNextTurn})
	}
	end := *tb.snapshot("ana").Round.PhaseDeadlineAt

	tb.send("bia", protocol.ClientMessage{Type: protocol.TypeExtendDiscussion})
	assert.Equal(t, protocol.ErrCodeNotHost, tb.clients["bia"].lastError())

	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeExtendDiscussion})
	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeExtendDiscussion})

	assert.Equal(t, end.Add(2*time.Minute), *tb.snapshot("bia").Round.PhaseDeadlineAt)
	assert.Equal(t, []time.Time{end.Add(time.Minute), end.Add(2 * time.Minute)}, tb.store.discussion)

	tb.clock.Advance(4 * time.Minute)
	tb.settle()
	assert.Equal(t, "discussion", tb.snapshot("ana").Round.Phase, "the extended discussion is still running")

	tb.clock.Advance(time.Minute)
	tb.settle()
	assert.Equal(t, "voting", tb.snapshot("ana").Round.Phase)
}

func TestDiscussion_ExtendOnlyDuringTheDiscussion(t *testing.T) {
	tb := newTable(t)
	tb.toQuestions()

	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeExtendDiscussion})

	assert.Equal(t, protocol.ErrCodeInvalidPhase, tb.clients["ana"].lastError())
	assert.Empty(t, tb.store.discussion)
}

func TestResume_ARoundLoadedFromTheDatabaseKeepsItsDeadline(t *testing.T) {
	h := newHarness(t)
	now := h.clock.Now()
	deadline := now.Add(-time.Second) // the turn ran out while nobody was connected
	h.store.state.Match.Status = game.MatchInProgress
	h.store.state.Round = &RoundState{
		ID: "round-1", Number: 1, Phase: game.PhaseQuestions,
		Theme:      &Theme{ID: 1, Name: "Comidas", Kind: game.ThemeKindWords},
		ImpostorID: "caio", Secret: "Pizza",
		Turns: []TurnState{
			{ID: "turn-0", Position: 0, AskerID: "ana", AnswererID: "bia", Question: "Q0?", DeadlineAt: &deadline},
			{ID: "turn-1", Position: 1, AskerID: "bia", AnswererID: "caio", Question: "Q1?"},
			{ID: "turn-2", Position: 2, AskerID: "caio", AnswererID: "ana", Question: "Q2?"},
		},
	}

	_, client := h.connect("t-bia")
	h.clock.Advance(0) // the fake clock runs due timers only when advanced
	h.settle()

	require.Len(t, h.store.advanced, 1)
	assert.Equal(t, endedByTimeout, h.store.advanced[0].EndedBy)
	s := client.lastSnapshot(t)
	require.Len(t, s.Round.Turns, 2)
	assert.Equal(t, "Pizza", *s.Me.Secret)
	assert.Equal(t, protocol.RoleInsider, *s.Me.Role)
}

// A timer-driven transition whose database write fails is retried: the round must not stay stuck
// in its phase with no deadline.
func TestTimers_RetryAfterADatabaseFailure(t *testing.T) {
	failNext := func(tb *table, method string) {
		tb.store.mu.Lock()
		tb.store.failures = map[string]int{method: 1}
		tb.store.mu.Unlock()
	}
	toDiscussion := func(tb *table) {
		tb.toQuestions()
		for range 3 {
			tb.send(tb.currentTurn().AnswererID, protocol.ClientMessage{Type: protocol.TypeNextTurn})
		}
		require.Equal(tb.t, "discussion", tb.snapshot("ana").Round.Phase)
	}

	t.Run("turn deadline", func(t *testing.T) {
		tb := newTable(t)
		tb.toQuestions()
		failNext(tb, "AdvanceTurn")
		tb.clock.Advance(3 * time.Minute)
		tb.settle()
		require.Empty(t, tb.store.advanced)

		tb.clock.Advance(storeRetryDelay)
		tb.settle()
		assert.Len(t, tb.store.advanced, 1)
	})

	t.Run("discussion deadline", func(t *testing.T) {
		tb := newTable(t)
		toDiscussion(tb)
		failNext(tb, "StartVoting")
		tb.clock.Advance(3 * time.Minute)
		tb.settle()
		require.Equal(t, "discussion", tb.snapshot("ana").Round.Phase)

		tb.clock.Advance(storeRetryDelay)
		tb.settle()
		assert.Equal(t, "voting", tb.snapshot("ana").Round.Phase)
	})

	t.Run("voting deadline", func(t *testing.T) {
		tb := newTable(t)
		tb.toVoting()
		failNext(tb, "FinishRound")
		tb.clock.Advance(game.VoteTimeout)
		tb.settle()
		require.Equal(t, "voting", tb.snapshot("ana").Round.Phase)

		tb.clock.Advance(storeRetryDelay)
		tb.settle()
		assert.Equal(t, "result", tb.snapshot("ana").Round.Phase)
	})

	t.Run("voting deadline opening the guess", func(t *testing.T) {
		tb := newTable(t)
		tb.toVoting()
		impostor := tb.impostor()
		insiders := tb.others(impostor)
		tb.vote(insiders[0], impostor)
		tb.vote(insiders[1], impostor)
		failNext(tb, "StartGuess")
		tb.clock.Advance(game.VoteTimeout)
		tb.settle()
		require.Equal(t, "voting", tb.snapshot("ana").Round.Phase)

		tb.clock.Advance(storeRetryDelay)
		tb.settle()
		assert.Equal(t, "guess", tb.snapshot("ana").Round.Phase)
	})

	t.Run("guess deadline", func(t *testing.T) {
		tb := newTable(t)
		tb.toVoting()
		tb.everyoneVotesForTheImpostor()
		require.Equal(t, "guess", tb.snapshot("ana").Round.Phase)
		failNext(tb, "FinishRound")
		tb.clock.Advance(game.GuessTimeout)
		tb.settle()
		require.Equal(t, "guess", tb.snapshot("ana").Round.Phase)

		tb.clock.Advance(storeRetryDelay)
		tb.settle()
		assert.Equal(t, "result", tb.snapshot("ana").Round.Phase)
	})
}

// Confirming the reveal again changes nothing, so it sends nothing: a player repeating it cannot
// make the room broadcast at will.
func TestConfirmReveal_RepeatedSendsNothing(t *testing.T) {
	tb := newTable(t)
	tb.selectTheme(1)
	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeConfirmReveal})
	tb.clients["bia"].mu.Lock()
	before := len(tb.clients["bia"].messages)
	tb.clients["bia"].mu.Unlock()

	for range 5 {
		tb.send("ana", protocol.ClientMessage{Type: protocol.TypeConfirmReveal})
	}

	tb.clients["bia"].mu.Lock()
	defer tb.clients["bia"].mu.Unlock()
	assert.Len(t, tb.clients["bia"].messages, before)
}
