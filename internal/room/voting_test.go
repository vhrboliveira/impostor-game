package room

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vhrboliveira/impostor-game/internal/game"
	"github.com/vhrboliveira/impostor-game/internal/protocol"
)

// toVoting plays the words theme through the questions and the discussion.
func (tb *table) toVoting() {
	tb.t.Helper()
	tb.toQuestions()
	for range 3 {
		tb.send(tb.currentTurn().AnswererID, protocol.ClientMessage{Type: protocol.TypeNextTurn})
	}
	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeStartVoting})
	require.Equal(tb.t, "voting", tb.snapshot("ana").Round.Phase)
}

func (tb *table) impostor() string {
	return tb.store.began[len(tb.store.began)-1].ImpostorID
}

// others returns the players of the table other than the given ones, in a stable order.
func (tb *table) others(except ...string) []string {
	var out []string
	for _, id := range []string{"ana", "bia", "caio"} {
		skip := false
		for _, e := range except {
			skip = skip || id == e
		}
		if !skip {
			out = append(out, id)
		}
	}
	return out
}

func (tb *table) vote(voter, target string) {
	tb.send(voter, protocol.ClientMessage{Type: protocol.TypeVote, Vote: &protocol.VotePayload{TargetID: target}})
}

// everyoneVotesForTheImpostor has both insiders vote for the impostor, who votes for an insider.
func (tb *table) everyoneVotesForTheImpostor() {
	impostor := tb.impostor()
	insiders := tb.others(impostor)
	tb.vote(insiders[0], impostor)
	tb.vote(insiders[1], impostor)
	tb.vote(impostor, insiders[0])
}

func (tb *table) guess(playerID, optionID string) {
	tb.send(playerID, protocol.ClientMessage{Type: protocol.TypeGuess, Guess: &protocol.GuessPayload{OptionID: optionID}})
}

func TestVote_Errors(t *testing.T) {
	tb := newTable(t)
	tb.toVoting()

	tb.vote("bia", "bia")
	assert.Equal(t, protocol.ErrCodeInvalidVote, tb.clients["bia"].lastError(), "nobody votes for themselves")

	tb.vote("bia", "stranger")
	assert.Equal(t, protocol.ErrCodeInvalidVote, tb.clients["bia"].lastError())

}

func TestVote_CanChangeWhileTheVotingIsOpen(t *testing.T) {
	tb := newTable(t)
	tb.toVoting()
	impostor := tb.impostor()
	voter := tb.others(impostor)[0]
	other := tb.others(impostor, voter)[0]

	tb.vote(voter, impostor)
	tb.vote(voter, impostor) // the same vote again changes nothing
	tb.vote(voter, other)

	assert.Empty(t, tb.clients[voter].lastError())
	assert.Equal(t, []string{voter + ">" + impostor, voter + ">" + other}, tb.store.castVotes)
	s := tb.snapshot(voter)
	require.NotNil(t, s.Me.VoteTargetID)
	assert.Equal(t, other, *s.Me.VoteTargetID)
	assert.Equal(t, game.PhaseVoting, game.Phase(s.Round.Phase), "one vote does not close it")
}

func TestVote_OnlyDuringTheVoting(t *testing.T) {
	tb := newTable(t)
	tb.toQuestions()

	tb.vote("bia", "caio")

	assert.Equal(t, protocol.ErrCodeInvalidPhase, tb.clients["bia"].lastError())
}

func TestVote_ProgressIsPublicButTargetsStaySecret(t *testing.T) {
	tb := newTable(t)
	tb.toVoting()

	tb.vote("bia", "caio")

	s := tb.snapshot("ana")
	assert.Equal(t, []string{"bia"}, s.Round.VotedIDs)
	assert.Nil(t, s.Round.Result)
	assert.Nil(t, s.Me.VoteTargetID, "Ana has not voted")
	assert.Equal(t, "caio", *tb.snapshot("bia").Me.VoteTargetID, "each player sees their own vote")
}

func TestVoting_FoundImpostorGuessesRightAndScores(t *testing.T) {
	tb := newTable(t)
	tb.toVoting()
	impostor := tb.impostor()
	insiders := tb.others(impostor)

	tb.everyoneVotesForTheImpostor()

	s := tb.snapshot(impostor)
	require.Equal(t, "guess", s.Round.Phase)
	assert.Nil(t, s.Round.Result, "the result waits for the guess")
	require.NotNil(t, s.Round.PhaseDeadlineAt)
	assert.Equal(t, tb.clock.Now().Add(game.GuessTimeout), *s.Round.PhaseDeadlineAt)
	require.Len(t, s.Round.GuessOptions, game.GuessOptions, "the theme has more words than options")
	var pizza string
	labels := map[string]bool{}
	for _, o := range s.Round.GuessOptions {
		labels[o.Label] = true
		if o.Label == "Pizza" {
			pizza = o.ID
		}
	}
	require.NotEmpty(t, pizza, "the secret is among the options")
	assert.Len(t, labels, game.GuessOptions, "options do not repeat")
	assert.Empty(t, tb.snapshot(insiders[0]).Round.GuessOptions, "only the impostor gets the options")

	tb.guess(insiders[0], pizza)
	assert.Equal(t, protocol.ErrCodeNotImpostor, tb.clients[insiders[0]].lastError())
	tb.guess(impostor, "999")
	assert.Equal(t, protocol.ErrCodeInvalidGuess, tb.clients[impostor].lastError())

	tb.guess(impostor, pizza)

	s = tb.snapshot(insiders[1])
	require.Equal(t, "result", s.Round.Phase)
	result := s.Round.Result
	require.NotNil(t, result)
	assert.Equal(t, impostor, result.ImpostorID)
	assert.True(t, result.ImpostorFound)
	assert.Equal(t, "Pizza", result.Secret)
	require.NotNil(t, result.Guess)
	assert.True(t, result.Guess.Correct)
	assert.Equal(t, "Pizza", *result.Guess.Label)
	require.Len(t, result.Votes, 3)
	for _, v := range result.Votes {
		require.NotNil(t, v.TargetID)
		if v.VoterID == impostor {
			assert.Equal(t, insiders[0], *v.TargetID)
		} else {
			assert.Equal(t, impostor, *v.TargetID)
		}
	}
	points := map[string]int{}
	for _, p := range result.Points {
		points[p.PlayerID] = p.Points
	}
	assert.Equal(t, map[string]int{insiders[0]: 1, insiders[1]: 1, impostor: 1}, points)
	for _, p := range s.Players {
		assert.Equal(t, 1, p.Score, "the scoreboard shows the totals of the match")
	}
	assert.True(t, tb.store.finished[0].GuessCorrect)
	assert.Equal(t, int64(11), tb.store.finished[0].GuessWordID)
}

func TestVoting_ATieLetsTheImpostorEscape(t *testing.T) {
	tb := newTable(t)
	tb.toVoting()
	impostor := tb.impostor()
	insiders := tb.others(impostor)

	// One vote each: the impostor ties with both insiders.
	tb.vote(insiders[0], impostor)
	tb.vote(insiders[1], insiders[0])
	tb.vote(impostor, insiders[1])

	s := tb.snapshot(impostor)
	require.Equal(t, "result", s.Round.Phase, "no guess when the impostor escaped")
	assert.False(t, s.Round.Result.ImpostorFound)
	assert.Nil(t, s.Round.Result.Guess)
	points := map[string]int{}
	for _, p := range s.Round.Result.Points {
		points[p.PlayerID] = p.Points
	}
	assert.Equal(t, map[string]int{impostor: 2, insiders[0]: 1}, points, "+2 escaped, +1 for the right vote")
	assert.Empty(t, tb.store.guesses)
}

func TestVoting_ClosesAtTheDeadlineWithAbstentions(t *testing.T) {
	tb := newTable(t)
	tb.toVoting()
	impostor := tb.impostor()
	voter := tb.others(impostor)[0]
	tb.vote(voter, impostor)

	tb.clock.Advance(game.VoteTimeout)
	tb.settle()

	require.Len(t, tb.store.guesses, 1, "one vote for the impostor and nothing else finds them")
	assert.ElementsMatch(t, tb.others(voter), tb.store.guesses[0].Abstainers)
	assert.Equal(t, "guess", tb.snapshot("ana").Round.Phase)
}

func TestVoting_AnOfflinePlayerDoesNotHoldTheVoting(t *testing.T) {
	tb := newTable(t)
	tb.toVoting()
	impostor := tb.impostor()
	insiders := tb.others(impostor)
	tb.vote(insiders[0], impostor)
	tb.vote(insiders[1], insiders[0])

	tb.conns[impostor].Leave()
	tb.settle()

	assert.Equal(t, "result", tb.snapshot(insiders[0]).Round.Phase)
	assert.Equal(t, []string{impostor}, tb.store.finished[0].Abstainers)
}

func TestGuess_TimeRunsOutAsAWrongGuess(t *testing.T) {
	tb := newTable(t)
	tb.toVoting()
	tb.everyoneVotesForTheImpostor()

	tb.clock.Advance(game.GuessTimeout)
	tb.settle()

	s := tb.snapshot("ana")
	require.Equal(t, "result", s.Round.Phase)
	require.NotNil(t, s.Round.Result.Guess)
	assert.False(t, s.Round.Result.Guess.Correct)
	assert.Nil(t, s.Round.Result.Guess.Label)
	for _, p := range s.Round.Result.Points {
		assert.NotEqual(t, tb.impostor(), p.PlayerID, "a wrong guess earns the impostor nothing")
	}
}

func TestGuess_PlayersThemeOffersTheOtherPlayers(t *testing.T) {
	tb := newTable(t)
	tb.selectTheme(2)
	for _, id := range []string{"ana", "bia", "caio"} {
		tb.send(id, protocol.ClientMessage{Type: protocol.TypeConfirmReveal})
	}
	for range 3 {
		tb.send(tb.currentTurn().AnswererID, protocol.ClientMessage{Type: protocol.TypeNextTurn})
	}
	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeStartVoting})
	plan := tb.store.began[0]

	tb.everyoneVotesForTheImpostor()

	options := tb.snapshot(plan.ImpostorID).Round.GuessOptions
	require.Len(t, options, 2, "every player but the impostor")
	ids := []string{options[0].ID, options[1].ID}
	assert.Contains(t, ids, plan.SubjectID)
	assert.NotContains(t, ids, plan.ImpostorID)

	tb.guess(plan.ImpostorID, plan.SubjectID)
	assert.True(t, tb.snapshot("ana").Round.Result.Guess.Correct)
	assert.Equal(t, plan.SubjectID, tb.store.finished[0].GuessPlayerID)
}

func TestNextRound(t *testing.T) {
	t.Run("only the host, only from the result", func(t *testing.T) {
		tb := newTable(t)
		tb.toVoting()

		tb.send("ana", protocol.ClientMessage{Type: protocol.TypeNextRound})
		assert.Equal(t, protocol.ErrCodeInvalidPhase, tb.clients["ana"].lastError())

		tb.everyoneVotesForTheImpostor()
		tb.clock.Advance(game.GuessTimeout)
		tb.settle()
		tb.send("bia", protocol.ClientMessage{Type: protocol.TypeNextRound})
		assert.Equal(t, protocol.ErrCodeNotHost, tb.clients["bia"].lastError())
	})

	t.Run("opens the theme selection of the next round with a new impostor", func(t *testing.T) {
		tb := newTable(t)
		tb.toVoting()
		first := tb.impostor()
		tb.everyoneVotesForTheImpostor()
		tb.clock.Advance(game.GuessTimeout)
		tb.settle()

		tb.send("ana", protocol.ClientMessage{Type: protocol.TypeNextRound})

		s := tb.snapshot("bia")
		assert.Equal(t, 2, s.Match.RoundNumber)
		require.NotNil(t, s.Round)
		assert.Equal(t, "theme_selection", s.Round.Phase)
		assert.Nil(t, s.Round.Result)
		assert.Nil(t, s.Me.Role)
		assert.Equal(t, []int{2}, tb.store.nextRounds)

		tb.store.candidates[1] = RoundCandidates{Words: wordList(12, "Sushi", "Lasanha", "Pastel", "Coxinha"), Questions: questionList("comida", 5)}
		tb.selectTheme(1)
		require.Len(t, tb.store.began, 2, "the next round starts")
		tb.m.mu.Lock()
		a := tb.m.actors["K7M2P"]
		tb.m.mu.Unlock()
		assert.Equal(t, []PastRound{{ImpostorID: first}}, a.state.PastRounds, "the rotation weighs the earlier impostor")
	})

	t.Run("after the last round it ends the match", func(t *testing.T) {
		tb := newTableWith(t, func(store *fakeLiveStore) { store.state.Match.TotalRounds = 3 })
		tb.toVoting()
		// Rounds 1 and 2 already happened: this is the last one.
		tb.m.mu.Lock()
		a := tb.m.actors["K7M2P"]
		tb.m.mu.Unlock()
		a.do(func() { a.state.Round.Number = 3 })
		tb.settle()
		tb.everyoneVotesForTheImpostor()
		tb.clock.Advance(game.GuessTimeout)
		tb.settle()

		tb.send("ana", protocol.ClientMessage{Type: protocol.TypeNextRound})

		assert.Equal(t, "finished", tb.snapshot("caio").Match.Status)
		assert.Equal(t, []game.MatchStatus{game.MatchFinished}, tb.store.endedMatches)
		assert.Empty(t, tb.store.nextRounds)
	})
}

func TestEndMatch(t *testing.T) {
	tb := newTable(t)
	tb.toQuestions()

	tb.send("bia", protocol.ClientMessage{Type: protocol.TypeEndMatch})
	assert.Equal(t, protocol.ErrCodeNotHost, tb.clients["bia"].lastError())

	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeEndMatch})

	assert.Equal(t, "ended_early", tb.snapshot("bia").Match.Status)
	assert.Equal(t, []game.MatchStatus{game.MatchEndedEarly}, tb.store.endedMatches)

	tb.clock.Advance(10 * time.Minute)
	tb.settle()
	assert.Empty(t, tb.store.advanced, "the turn timer stopped with the match")

	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeEndMatch})
	assert.Equal(t, protocol.ErrCodeInvalidPhase, tb.clients["ana"].lastError())
}

func TestPlayAgain(t *testing.T) {
	playAgain := func(tb *table, playerID string, rounds int) {
		tb.send(playerID, protocol.ClientMessage{Type: protocol.TypePlayAgain, PlayAgain: &protocol.PlayAgainPayload{TotalRounds: rounds}})
	}

	t.Run("only after the match ended", func(t *testing.T) {
		tb := newTable(t)
		playAgain(tb, "ana", 5)
		assert.Equal(t, protocol.ErrCodeInvalidPhase, tb.clients["ana"].lastError())
	})

	t.Run("starts a new match in the lobby with the scores at zero", func(t *testing.T) {
		tb := newTable(t)
		tb.toVoting()
		tb.everyoneVotesForTheImpostor()
		tb.clock.Advance(game.GuessTimeout)
		tb.settle()
		tb.send("ana", protocol.ClientMessage{Type: protocol.TypeEndMatch})

		playAgain(tb, "bia", 5)
		assert.Equal(t, protocol.ErrCodeNotHost, tb.clients["bia"].lastError())
		playAgain(tb, "ana", 4)
		assert.Equal(t, protocol.ErrCodeInvalidRounds, tb.clients["ana"].lastError())

		playAgain(tb, "ana", 8)

		s := tb.snapshot("caio")
		assert.Equal(t, protocol.MatchView{Status: "lobby", TotalRounds: 8, RoundNumber: 0}, s.Match)
		assert.Nil(t, s.Round)
		for _, p := range s.Players {
			assert.Zero(t, p.Score)
		}
		require.Len(t, tb.store.playAgains, 1)
		assert.Equal(t, 2, tb.store.playAgains[0].Number)

		tb.send("ana", protocol.ClientMessage{Type: protocol.TypeStartMatch})
		assert.Equal(t, "theme_selection", tb.snapshot("bia").Round.Phase, "the new match starts like the first")
	})
}

func TestResume_AGuessLoadedFromTheDatabaseTimesOut(t *testing.T) {
	h := newHarness(t)
	deadline := h.clock.Now().Add(-time.Second)
	h.store.state.Match.Status = game.MatchInProgress
	h.store.state.Round = &RoundState{
		ID: "round-1", Number: 1, Phase: game.PhaseGuess,
		Theme:      &Theme{ID: 1, Name: "Comidas", Kind: game.ThemeKindWords},
		ImpostorID: "caio", WordID: 11, Secret: "Pizza",
		Turns: []TurnState{
			{ID: "turn-0", Position: 0, AskerID: "ana", AnswererID: "bia", Question: "Q0?"},
			{ID: "turn-1", Position: 1, AskerID: "bia", AnswererID: "caio", Question: "Q1?"},
			{ID: "turn-2", Position: 2, AskerID: "caio", AnswererID: "ana", Question: "Q2?"},
		},
		PhaseDeadline: &deadline,
		Votes:         map[string]string{"ana": "caio", "bia": "caio", "caio": "ana"},
		ImpostorFound: true,
		GuessOptions:  []GuessOption{{ID: "11", Label: "Pizza"}, {ID: "12", Label: "Sushi"}},
	}

	_, client := h.connect("t-ana")
	h.clock.Advance(0)
	h.settle()

	require.Len(t, h.store.finished, 1)
	s := client.lastSnapshot(t)
	assert.Equal(t, "result", s.Round.Phase)
	assert.False(t, s.Round.Result.Guess.Correct)
}

// An ended match freezes its round: no round command, presence change or timer of that match
// runs afterwards, and its scores are final.
func TestEndMatch_FreezesTheRound(t *testing.T) {
	end := func(tb *table) {
		tb.send("ana", protocol.ClientMessage{Type: protocol.TypeEndMatch})
		require.Equal(tb.t, "ended_early", tb.snapshot("ana").Match.Status)
	}

	t.Run("turn commands are refused", func(t *testing.T) {
		tb := newTable(t)
		tb.toQuestions()
		answerer := tb.currentTurn().AnswererID
		end(tb)

		tb.send(answerer, protocol.ClientMessage{Type: protocol.TypeNextTurn})
		assert.Equal(t, protocol.ErrCodeInvalidPhase, tb.clients[answerer].lastError())
		tb.send(answerer, protocol.ClientMessage{Type: protocol.TypeExtendTurn})
		assert.Equal(t, protocol.ErrCodeInvalidPhase, tb.clients[answerer].lastError())
		tb.send("ana", protocol.ClientMessage{Type: protocol.TypeNextTurn})
		assert.Equal(t, protocol.ErrCodeInvalidPhase, tb.clients["ana"].lastError())
		assert.Empty(t, tb.store.advanced)
		assert.Empty(t, tb.store.extended)
	})

	t.Run("the reveal is not confirmed nor started", func(t *testing.T) {
		tb := newTable(t)
		tb.selectTheme(1)
		tb.send("ana", protocol.ClientMessage{Type: protocol.TypeConfirmReveal})
		tb.send("bia", protocol.ClientMessage{Type: protocol.TypeConfirmReveal})
		end(tb)

		tb.send("caio", protocol.ClientMessage{Type: protocol.TypeConfirmReveal})
		assert.Equal(t, protocol.ErrCodeInvalidPhase, tb.clients["caio"].lastError())
		// The only player who had not confirmed leaves: an ended match must not start its questions.
		tb.conns["caio"].Leave()
		tb.settle()
		assert.Empty(t, tb.store.questions)
	})

	t.Run("votes do not change the podium", func(t *testing.T) {
		tb := newTable(t)
		tb.toVoting()
		end(tb)

		tb.everyoneVotesForTheImpostor()
		assert.Equal(t, protocol.ErrCodeInvalidPhase, tb.clients[tb.impostor()].lastError())
		assert.Empty(t, tb.store.castVotes)
		assert.Empty(t, tb.store.finished)
		for _, p := range tb.snapshot("ana").Players {
			assert.Zero(t, p.Score, p.ID)
		}
	})

	t.Run("the guess is refused", func(t *testing.T) {
		tb := newTable(t)
		tb.toVoting()
		tb.everyoneVotesForTheImpostor()
		require.Equal(t, "guess", tb.snapshot("ana").Round.Phase)
		end(tb)

		options := tb.snapshot(tb.impostor()).Round.GuessOptions
		require.NotEmpty(t, options)
		tb.guess(tb.impostor(), options[0].ID)
		assert.Equal(t, protocol.ErrCodeInvalidPhase, tb.clients[tb.impostor()].lastError())
		assert.Empty(t, tb.store.finished)
	})
}

// "Jogar de novo" after an early end must start clean: no timer of the old round may fire on the
// new match (it used to dereference a nil round and crash the whole process).
func TestPlayAgain_AfterEndMatchFiresNoStaleTimer(t *testing.T) {
	tb := newTable(t)
	tb.selectTheme(1)
	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeConfirmReveal})
	tb.send("bia", protocol.ClientMessage{Type: protocol.TypeConfirmReveal})
	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeEndMatch})
	tb.conns["caio"].Leave() // honest player closes the app on the podium
	tb.settle()
	tb.conns["caio"], tb.clients["caio"] = tb.connect("t-caio")

	tb.send("ana", protocol.ClientMessage{Type: protocol.TypePlayAgain, PlayAgain: &protocol.PlayAgainPayload{TotalRounds: 5}})
	require.Equal(t, "lobby", tb.snapshot("ana").Match.Status)
	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeStartMatch})
	require.Equal(t, "theme_selection", tb.snapshot("ana").Round.Phase)

	tb.clock.Advance(10 * time.Minute)
	tb.settle()
	assert.Empty(t, tb.store.advanced)
	assert.Equal(t, "theme_selection", tb.snapshot("ana").Round.Phase)
}

// Guess decoys come from the same words the secret could be drawn from: words no other round of
// the room used. An earlier secret, already revealed in its result, is never offered.
func TestGuessOptions_DecoysAreUnusedWords(t *testing.T) {
	tb := newTableWith(t, func(f *fakeLiveStore) {
		f.themeWords[1] = wordList(11, "Pizza", "Sushi", "Lasanha", "Pastel", "Coxinha")
		f.candidates[1] = RoundCandidates{Words: wordList(11, "Pizza", "Sushi", "Lasanha", "Pastel", "Coxinha"), Questions: questionList("comida", 5)}
	})
	tb.toVoting()
	first := tb.store.began[0].WordID
	tb.everyoneVotesForTheImpostor()
	tb.guess(tb.impostor(), strconv.FormatInt(first, 10))
	tb.send("ana", protocol.ClientMessage{Type: protocol.TypeNextRound})

	var unused []Word
	for _, w := range tb.store.themeWords[1] {
		if w.ID != first {
			unused = append(unused, w)
		}
	}
	tb.store.candidates[1] = RoundCandidates{Words: unused, Questions: questionList("comida", 5)}
	tb.toVoting()
	tb.everyoneVotesForTheImpostor()

	options := tb.snapshot(tb.impostor()).Round.GuessOptions
	require.NotEmpty(t, options)
	for _, o := range options {
		assert.NotEqual(t, strconv.FormatInt(first, 10), o.ID, "the first round's secret is not a possible answer")
	}
}

// A words theme needs game.MinGuessWords unused words: with fewer, the found impostor's guess
// would narrow down to the secret.
func TestSelectTheme_TooFewUnusedWords(t *testing.T) {
	tb := newTableWith(t, func(f *fakeLiveStore) {
		f.candidates[1] = RoundCandidates{Words: wordList(11, "Pizza", "Sushi", "Lasanha")[:game.MinGuessWords-1], Questions: questionList("comida", 5)}
	})
	tb.selectTheme(1)
	assert.Equal(t, protocol.ErrCodeThemeUnavailable, tb.clients["ana"].lastError())
	assert.Empty(t, tb.store.began)
}
