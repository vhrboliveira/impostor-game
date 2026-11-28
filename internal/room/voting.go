package room

import (
	"slices"
	"strconv"

	"github.com/vhrboliveira/impostor-game/internal/game"
	"github.com/vhrboliveira/impostor-game/internal/protocol"
)

// startVoting moves the discussion to the voting, which closes after game.VoteTimeout or as soon
// as every connected player of the round voted. c receives the error, if any; it is nil when the
// discussion timer started the voting.
func (a *actor) startVoting(c Client) {
	round := a.state.Round
	deadline := a.m.clock.Now().Add(game.VoteTimeout)

	ctx, cancel := storeContext()
	defer cancel()
	if err := a.m.store.StartVoting(ctx, round.ID, deadline); err != nil {
		a.failCommand(c, "start voting", err)
		if c == nil {
			a.retryLater(func() { a.startVoting(nil) })
		}
		return
	}

	round.Phase = game.PhaseVoting
	round.PhaseDeadline = &deadline
	round.Votes = make(map[string]string)
	a.armPhaseDeadline(a.closeVoting)
	a.broadcast()
}

// vote records a player's vote, or changes it: a player can change their vote while the voting is
// open (it closes when every connected player of the round voted, or at the deadline). Voting for
// oneself or for someone outside the round is refused.
func (a *actor) vote(playerID string, c Client, payload *protocol.VotePayload) {
	round := a.activeRound()
	switch {
	case round == nil || round.Phase != game.PhaseVoting:
		c.Send(protocol.ErrorMessage(protocol.ErrCodeInvalidPhase))
		return
	case !round.hasPlayer(playerID):
		c.Send(protocol.ErrorMessage(protocol.ErrCodeNotParticipant))
		return
	case payload.TargetID == playerID || !round.hasPlayer(payload.TargetID):
		c.Send(protocol.ErrorMessage(protocol.ErrCodeInvalidVote))
		return
	case round.Votes[playerID] == payload.TargetID:
		return // the same vote again: nothing changes
	}

	ctx, cancel := storeContext()
	defer cancel()
	if err := a.m.store.CastVote(ctx, round.ID, playerID, payload.TargetID, a.m.clock.Now()); err != nil {
		a.failCommand(c, "cast vote", err)
		return
	}

	round.Votes[playerID] = payload.TargetID
	if !a.closeVotingIfAllVoted() {
		a.broadcast()
	}
}

// closeVotingIfAllVoted closes the voting when every connected player of the round voted.
// Offline players never hold it. It reports whether it closed it (and broadcast).
func (a *actor) closeVotingIfAllVoted() bool {
	round := a.activeRound()
	if round == nil || round.Phase != game.PhaseVoting {
		return false
	}
	connected := 0
	for _, id := range round.playerIDs() {
		if !a.isConnected(id) {
			continue
		}
		connected++
		if round.Votes[id] == "" {
			return false
		}
	}
	if connected == 0 {
		return false
	}
	a.closeVoting()
	return true
}

// closeVoting counts the votes. When the impostor was found they get the guess; otherwise the
// round goes straight to the result.
func (a *actor) closeVoting() {
	round := a.state.Round
	var abstainers []string
	for _, id := range round.playerIDs() {
		if _, voted := round.Votes[id]; !voted {
			abstainers = append(abstainers, id)
		}
	}

	tally := game.CountVotes(round.votes(), game.PlayerID(round.ImpostorID))
	if !tally.ImpostorFound {
		if !a.finishRound(nil, abstainers, false, nil) {
			a.retryLater(a.closeVoting)
		}
		return
	}

	options, err := a.guessOptions(round)
	if err != nil {
		a.logError("guess options", err)
		a.retryLater(a.closeVoting)
		return
	}
	ids := make([]string, len(options))
	for i, o := range options {
		ids[i] = o.ID
	}
	deadline := a.m.clock.Now().Add(game.GuessTimeout)

	ctx, cancel := storeContext()
	defer cancel()
	if err := a.m.store.StartGuess(ctx, StartGuessParams{
		RoundID:    round.ID,
		Abstainers: abstainers,
		OptionIDs:  ids,
		Deadline:   deadline,
	}); err != nil {
		a.logError("start guess", err)
		a.retryLater(a.closeVoting)
		return
	}

	for _, id := range abstainers {
		round.Votes[id] = ""
	}
	round.Phase = game.PhaseGuess
	round.ImpostorFound = true
	round.GuessOptions = options
	round.PhaseDeadline = &deadline
	a.armPhaseDeadline(a.guessTimedOut)
	a.broadcast()
}

// guessOptions draws up to game.GuessOptions possible secrets, the right one among them: words of
// the theme no other round of the room used, or the players of the round other than the impostor
// in a players theme.
func (a *actor) guessOptions(round *RoundState) ([]GuessOption, error) {
	var (
		right  GuessOption
		others []GuessOption
	)
	if round.Theme.Kind == game.ThemeKindPlayers {
		for _, id := range round.playerIDs() {
			option := GuessOption{ID: id, Label: a.player(id).Name}
			switch id {
			case round.SubjectID:
				right = option
			case round.ImpostorID:
			default:
				others = append(others, option)
			}
		}
	} else {
		ctx, cancel := storeContext()
		defer cancel()
		// The same unused words the secret came from: an earlier secret of the room, already
		// revealed in its result, would be an option the impostor can rule out.
		words, err := a.m.store.LoadGuessWords(ctx, a.state.RoomID, round.Theme.ID, round.ID)
		if err != nil {
			return nil, err
		}
		for _, w := range words {
			option := GuessOption{ID: strconv.FormatInt(w.ID, 10), Label: w.Text}
			if w.ID == round.WordID {
				right = option
			} else {
				others = append(others, option)
			}
		}
	}

	a.rng.Shuffle(len(others), func(i, j int) { others[i], others[j] = others[j], others[i] })
	options := append([]GuessOption{right}, others[:min(len(others), game.GuessOptions-1)]...)
	a.rng.Shuffle(len(options), func(i, j int) { options[i], options[j] = options[j], options[i] })
	return options, nil
}

// guess is the found impostor's pick.
func (a *actor) guess(playerID string, c Client, payload *protocol.GuessPayload) {
	round := a.activeRound()
	switch {
	case round == nil || round.Phase != game.PhaseGuess:
		c.Send(protocol.ErrorMessage(protocol.ErrCodeInvalidPhase))
		return
	case playerID != round.ImpostorID:
		c.Send(protocol.ErrorMessage(protocol.ErrCodeNotImpostor))
		return
	case !slices.ContainsFunc(round.GuessOptions, func(o GuessOption) bool { return o.ID == payload.OptionID }):
		c.Send(protocol.ErrorMessage(protocol.ErrCodeInvalidGuess))
		return
	}

	a.finishRound(c, nil, true, &GuessState{
		OptionID: payload.OptionID,
		Correct:  payload.OptionID == round.secretOptionID(),
	})
}

// guessTimedOut ends a guess the impostor did not make in time: a wrong guess.
func (a *actor) guessTimedOut() {
	if !a.finishRound(nil, nil, true, &GuessState{}) {
		a.retryLater(a.guessTimedOut)
	}
}

// finishRound scores the round and moves it to the result. abstainers are the players who did
// not vote, when the round comes straight from the voting. It reports whether the round was
// written; on failure the round stays as it was.
func (a *actor) finishRound(c Client, abstainers []string, found bool, guess *GuessState) bool {
	round := a.state.Round
	for _, id := range abstainers {
		round.Votes[id] = ""
	}
	guessed := guess != nil && guess.Correct
	scores := game.ScoreRound(round.votes(), game.PlayerID(round.ImpostorID), found, guessed)

	params := FinishRoundParams{
		RoundID:       round.ID,
		Abstainers:    abstainers,
		ImpostorFound: found,
		GuessCorrect:  guessed,
		Scores:        scores,
	}
	if guess != nil && guess.OptionID != "" {
		if round.Theme.Kind == game.ThemeKindPlayers {
			params.GuessPlayerID = guess.OptionID
		} else {
			params.GuessWordID, _ = strconv.ParseInt(guess.OptionID, 10, 64)
		}
	}

	ctx, cancel := storeContext()
	defer cancel()
	if err := a.m.store.FinishRound(ctx, params); err != nil {
		for _, id := range abstainers {
			delete(round.Votes, id)
		}
		a.failCommand(c, "finish round", err)
		return false
	}

	a.phaseTimer.cancel()
	round.Phase = game.PhaseResult
	round.PhaseDeadline = nil
	round.ImpostorFound = found
	round.Guess = guess
	round.Points = make(map[string]int)
	for _, s := range scores {
		round.Points[string(s.Player)] += s.Points
	}
	if err := a.reloadPlayers(); err != nil {
		a.logError("reload scores", err)
	}
	a.broadcast()
	return true
}

// nextRound is the host's "Próxima rodada": a new round in theme selection, or the end of the
// match after the last round ("Ver pódio").
func (a *actor) nextRound(playerID string, c Client) {
	round, ok := a.requireHostInPhase(playerID, c, game.PhaseResult)
	if !ok {
		return
	}
	if round.Number >= a.state.Match.TotalRounds {
		a.finishMatch(c, game.MatchFinished)
		return
	}

	ctx, cancel := storeContext()
	defer cancel()
	id, err := a.m.store.NextRound(ctx, a.state.Match.ID, round.Number+1, a.m.clock.Now())
	if err != nil {
		a.failCommand(c, "next round", err)
		return
	}

	a.phaseTimer.cancel()
	a.state.PastRounds = append(a.state.PastRounds, PastRound{ImpostorID: round.ImpostorID, SubjectID: round.SubjectID})
	a.state.Round = &RoundState{ID: id, Number: round.Number + 1, Phase: game.PhaseThemeSelection}
	a.confirmed = nil
	a.broadcast()
}

// endMatch is the host's "Encerrar partida" (the confirmation step is on the web app).
func (a *actor) endMatch(playerID string, c Client) {
	if p := a.player(playerID); p == nil || !p.IsHost {
		c.Send(protocol.ErrorMessage(protocol.ErrCodeNotHost))
		return
	}
	if a.state.Match.Status != game.MatchInProgress {
		c.Send(protocol.ErrorMessage(protocol.ErrCodeInvalidPhase))
		return
	}
	a.finishMatch(c, game.MatchEndedEarly)
}

// finishMatch closes the match; everyone sees the podium.
func (a *actor) finishMatch(c Client, status game.MatchStatus) {
	ctx, cancel := storeContext()
	defer cancel()
	if err := a.m.store.EndMatch(ctx, a.state.Match.ID, status, a.m.clock.Now()); err != nil {
		a.failCommand(c, "end match", err)
		return
	}

	a.phaseTimer.cancel()
	a.state.Match.Status = status
	a.broadcast()
}

// playAgain is the host's "Jogar de novo": a new match in the lobby, same room and players,
// scores back to zero.
func (a *actor) playAgain(playerID string, c Client, payload *protocol.PlayAgainPayload) {
	if p := a.player(playerID); p == nil || !p.IsHost {
		c.Send(protocol.ErrorMessage(protocol.ErrCodeNotHost))
		return
	}
	if status := a.state.Match.Status; status != game.MatchFinished && status != game.MatchEndedEarly {
		c.Send(protocol.ErrorMessage(protocol.ErrCodeInvalidPhase))
		return
	}
	if !game.ValidRounds(payload.TotalRounds) {
		c.Send(protocol.ErrorMessage(protocol.ErrCodeInvalidRounds))
		return
	}

	ctx, cancel := storeContext()
	defer cancel()
	match, err := a.m.store.PlayAgain(ctx, PlayAgainParams{
		RoomID:      a.state.RoomID,
		Number:      a.state.Match.Number + 1,
		TotalRounds: payload.TotalRounds,
		TurnSeconds: a.state.Match.TurnSeconds,
		Now:         a.m.clock.Now(),
	})
	if err != nil {
		a.failCommand(c, "play again", err)
		return
	}

	a.phaseTimer.cancel()
	a.state.Match = match
	a.state.Round = nil
	a.state.PastRounds = nil
	a.confirmed = nil
	if err := a.reloadPlayers(); err != nil {
		a.logError("reload players", err)
	}
	a.broadcast()
}

// playerIDs lists the players of the round in ring order.
func (r *RoundState) playerIDs() []string {
	ids := make([]string, len(r.Turns))
	for i, turn := range r.Turns {
		ids[i] = turn.AskerID
	}
	return ids
}

// votes converts the recorded votes for the rules of package game, in ring order.
func (r *RoundState) votes() []game.Vote {
	var votes []game.Vote
	for _, id := range r.playerIDs() {
		if target, ok := r.Votes[id]; ok {
			votes = append(votes, game.Vote{Voter: game.PlayerID(id), Target: game.PlayerID(target)})
		}
	}
	return votes
}

// secretOptionID is the guess option that matches the secret.
func (r *RoundState) secretOptionID() string {
	if r.Theme != nil && r.Theme.Kind == game.ThemeKindPlayers {
		return r.SubjectID
	}
	return strconv.FormatInt(r.WordID, 10)
}
