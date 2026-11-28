package room

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/vhrboliveira/impostor-game/internal/game"
	"github.com/vhrboliveira/impostor-game/internal/protocol"
)

// Values of turns.ended_by.
const (
	endedByAnswerer = "answerer"
	endedByHost     = "host"
	endedByTimeout  = "timeout"
)

// errThemeUnavailable means a theme cannot feed a new round: no unused word, or fewer
// questions than players.
var errThemeUnavailable = errors.New("theme unavailable")

// selectTheme starts the round with the theme the host picked (or a random one): it draws the
// impostor and the secret, builds the turn ring with the connected players and moves to the
// reveal.
func (a *actor) selectTheme(playerID string, c Client, payload *protocol.SelectThemePayload) {
	round, ok := a.requireHostInPhase(playerID, c, game.PhaseThemeSelection)
	if !ok {
		return
	}

	players := a.connectedPlayerIDs()
	if len(players) < game.MinPlayers {
		c.Send(protocol.ErrorMessage(protocol.ErrCodeNotEnoughPlayers))
		return
	}

	var candidates []Theme
	if payload.Random {
		candidates = slices.Clone(a.state.Themes)
		a.rng.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
	} else if theme, found := a.theme(payload.ThemeID); found {
		candidates = []Theme{theme}
	}

	for _, theme := range candidates {
		err := a.beginRound(round, theme, payload.Random, players)
		if errors.Is(err, errThemeUnavailable) {
			continue // the random draw tries the next theme
		}
		if err != nil {
			a.logError("begin round", err)
			c.Send(protocol.ErrorMessage(protocol.ErrCodeInternal))
			return
		}
		a.broadcast()
		return
	}
	c.Send(protocol.ErrorMessage(protocol.ErrCodeThemeUnavailable))
}

// beginRound plans the round with one theme and writes it. It returns errThemeUnavailable when
// the theme cannot feed the round, leaving everything unchanged.
func (a *actor) beginRound(round *RoundState, theme Theme, drawn bool, players []string) error {
	ctx, cancel := storeContext()
	defer cancel()
	candidates, err := a.m.store.LoadRoundCandidates(ctx, a.state.RoomID, theme)
	if err != nil {
		return fmt.Errorf("load candidates: %w", err)
	}
	if len(candidates.Questions) < len(players) {
		return errThemeUnavailable
	}

	ids := toPlayerIDs(players)
	impostor, err := game.PickImpostor(ids, a.history(func(r PastRound) string { return r.ImpostorID }), a.rng)
	if err != nil {
		return fmt.Errorf("pick impostor: %w", err)
	}

	params := BeginRoundParams{
		RoundID:    round.ID,
		ThemeID:    theme.ID,
		ThemeDrawn: drawn,
		ImpostorID: string(impostor),
	}
	var secret string
	switch theme.Kind {
	case game.ThemeKindPlayers:
		subject, err := game.PickSubject(ids, a.history(func(r PastRound) string { return r.SubjectID }), impostor, a.rng)
		if err != nil {
			return fmt.Errorf("pick subject: %w", err)
		}
		params.SubjectID = string(subject)
		secret = a.player(params.SubjectID).Name
	default:
		// The guess options come from these same unused words.
		if len(candidates.Words) < game.MinGuessWords {
			return errThemeUnavailable
		}
		word := candidates.Words[a.rng.IntN(len(candidates.Words))]
		params.WordID = word.ID
		secret = word.Text
	}

	ring, err := game.BuildTurnRing(ids, a.rng)
	if err != nil {
		return fmt.Errorf("build turn ring: %w", err)
	}
	questions := slices.Clone(candidates.Questions)
	a.rng.Shuffle(len(questions), func(i, j int) { questions[i], questions[j] = questions[j], questions[i] })
	for i, pair := range ring {
		params.Turns = append(params.Turns, PlannedTurn{
			AskerID:    string(pair.Asker),
			AnswererID: string(pair.Answerer),
			QuestionID: questions[i].ID,
		})
	}

	turnIDs, err := a.m.store.BeginRound(ctx, params)
	if err != nil {
		return fmt.Errorf("write round: %w", err)
	}

	turns := make([]TurnState, len(params.Turns))
	for i, planned := range params.Turns {
		turns[i] = TurnState{
			ID:         turnIDs[i],
			Position:   i,
			AskerID:    planned.AskerID,
			AnswererID: planned.AnswererID,
			Question:   questions[i].Text,
		}
	}
	round.Phase = game.PhaseReveal
	round.Theme = &theme
	round.ThemeDrawn = drawn
	round.ImpostorID = params.ImpostorID
	round.WordID = params.WordID
	round.SubjectID = params.SubjectID
	round.Secret = secret
	round.Turns = turns
	round.CurrentTurn = 0
	a.confirmed = make(map[string]bool)
	return nil
}

// confirmReveal records that a player saw their role ("Entendi"). The questions start once every
// connected player of the round confirmed.
func (a *actor) confirmReveal(playerID string, c Client) {
	round := a.activeRound()
	if round == nil || round.Phase != game.PhaseReveal {
		c.Send(protocol.ErrorMessage(protocol.ErrCodeInvalidPhase))
		return
	}
	if !round.hasPlayer(playerID) {
		c.Send(protocol.ErrorMessage(protocol.ErrCodeNotParticipant))
		return
	}

	if a.confirmed[playerID] {
		return // already confirmed: nothing changed, nothing to send
	}
	a.confirmed[playerID] = true
	if !a.startQuestionsIfAllConfirmed() {
		a.broadcast()
	}
}

// startQuestionsIfAllConfirmed starts the questions when every connected player of the round
// confirmed the reveal. Offline players do not hold the round. It reports whether it started
// them (and broadcast).
func (a *actor) startQuestionsIfAllConfirmed() bool {
	round := a.activeRound()
	if round == nil || round.Phase != game.PhaseReveal {
		return false
	}
	connected := 0
	for _, turn := range round.Turns {
		if !a.isConnected(turn.AskerID) {
			continue
		}
		connected++
		if !a.confirmed[turn.AskerID] {
			return false
		}
	}
	if connected == 0 {
		return false
	}
	a.startQuestions(nil)
	return true
}

// forceStartQuestions lets the host skip the wait for confirmations.
func (a *actor) forceStartQuestions(playerID string, c Client) {
	if _, ok := a.requireHostInPhase(playerID, c, game.PhaseReveal); !ok {
		return
	}
	a.startQuestions(c)
}

// startQuestions moves the round to its first turn. c receives the error, if any; it is nil
// when no player asked for it directly.
func (a *actor) startQuestions(c Client) {
	round := a.state.Round
	now := a.m.clock.Now()
	deadline := now.Add(a.turnDuration())
	first := &round.Turns[0]

	ctx, cancel := storeContext()
	defer cancel()
	if err := a.m.store.StartQuestions(ctx, round.ID, first.ID, now, deadline); err != nil {
		a.failCommand(c, "start questions", err)
		if c == nil {
			a.retryLater(func() { a.startQuestions(nil) })
		}
		return
	}

	round.Phase = game.PhaseQuestions
	round.CurrentTurn = 0
	first.DeadlineAt = &deadline
	a.confirmed = nil
	a.armTurnDeadline()
	a.broadcast()
}

// nextTurn is "Prosseguir": the answerer (or the host, as a backup) ends the turn.
func (a *actor) nextTurn(playerID string, c Client) {
	turn, ok := a.requireTurnController(playerID, c)
	if !ok {
		return
	}
	endedBy := endedByHost
	if playerID == turn.AnswererID {
		endedBy = endedByAnswerer
	}
	a.advanceTurn(c, endedBy)
}

// extendTurn adds game.TurnExtension to the turn in progress.
func (a *actor) extendTurn(playerID string, c Client) {
	turn, ok := a.requireTurnController(playerID, c)
	if !ok {
		return
	}
	deadline := turn.DeadlineAt.Add(game.TurnExtension)

	ctx, cancel := storeContext()
	defer cancel()
	if err := a.m.store.ExtendTurn(ctx, turn.ID, deadline); err != nil {
		a.failCommand(c, "extend turn", err)
		return
	}

	turn.DeadlineAt = &deadline
	a.armTurnDeadline()
	a.broadcast()
}

// advanceTurn ends the turn in progress and starts the next one, or the discussion after the
// last turn.
func (a *actor) advanceTurn(c Client, endedBy string) {
	round := a.state.Round
	now := a.m.clock.Now()
	current := &round.Turns[round.CurrentTurn]
	params := AdvanceTurnParams{
		RoundID:     round.ID,
		EndedTurnID: current.ID,
		EndedBy:     endedBy,
		Now:         now,
	}

	last := round.CurrentTurn == len(round.Turns)-1
	if last {
		params.DiscussionDeadline = now.Add(a.m.cfg.DiscussionDuration)
	} else {
		next := round.Turns[round.CurrentTurn+1]
		params.NextTurnID = next.ID
		params.NextPosition = next.Position
		params.NextDeadline = now.Add(a.turnDuration())
	}

	ctx, cancel := storeContext()
	defer cancel()
	if err := a.m.store.AdvanceTurn(ctx, params); err != nil {
		a.failCommand(c, "advance turn", err)
		if c == nil {
			a.retryLater(func() { a.advanceTurn(nil, endedBy) })
		}
		return
	}

	current.DeadlineAt = nil
	if last {
		round.Phase = game.PhaseDiscussion
		round.PhaseDeadline = &params.DiscussionDeadline
		a.armDiscussionDeadline()
	} else {
		round.CurrentTurn++
		round.Turns[round.CurrentTurn].DeadlineAt = &params.NextDeadline
		a.armTurnDeadline()
	}
	a.broadcast()
}

// extendDiscussion lets the host add game.DiscussionExtension to the discussion.
func (a *actor) extendDiscussion(playerID string, c Client) {
	round, ok := a.requireHostInPhase(playerID, c, game.PhaseDiscussion)
	if !ok {
		return
	}
	deadline := round.PhaseDeadline.Add(game.DiscussionExtension)

	ctx, cancel := storeContext()
	defer cancel()
	if err := a.m.store.ExtendDiscussion(ctx, round.ID, deadline); err != nil {
		a.failCommand(c, "extend discussion", err)
		return
	}

	round.PhaseDeadline = &deadline
	a.armDiscussionDeadline()
	a.broadcast()
}

// endDiscussion lets the host start the voting before the discussion timer ends.
func (a *actor) endDiscussion(playerID string, c Client) {
	if _, ok := a.requireHostInPhase(playerID, c, game.PhaseDiscussion); !ok {
		return
	}
	a.startVoting(c)
}

// resumeRound re-arms the timer of a round loaded from the database (after a restart or after
// the actor stopped while idle). A deadline already in the past fires right away.
func (a *actor) resumeRound() {
	// The round of a finished or ended match stays frozen in the phase it had.
	round := a.activeRound()
	if round == nil {
		return
	}
	switch round.Phase {
	case game.PhaseReveal:
		a.confirmed = make(map[string]bool) // confirmations live in memory only
	case game.PhaseQuestions:
		a.armTurnDeadline()
	case game.PhaseDiscussion:
		a.armDiscussionDeadline()
	case game.PhaseVoting, game.PhaseGuess:
		if round.Votes == nil {
			// Storage leaves the map nil while the round has no vote row yet.
			round.Votes = make(map[string]string)
		}
		if round.Phase == game.PhaseVoting {
			a.armPhaseDeadline(a.closeVoting)
		} else {
			a.armPhaseDeadline(a.guessTimedOut)
		}
	}
}

// armDiscussionDeadline schedules the automatic start of the voting.
func (a *actor) armDiscussionDeadline() {
	a.armPhaseDeadline(func() { a.startVoting(nil) })
}

// armPhaseDeadline schedules fn at the round's phase deadline. The callback does nothing unless
// the same round of a match in progress is still in the same phase.
func (a *actor) armPhaseDeadline(fn func()) {
	round := a.state.Round
	if round.PhaseDeadline == nil {
		return
	}
	roundID, phase := round.ID, round.Phase
	a.arm(&a.phaseTimer, round.PhaseDeadline.Sub(a.m.clock.Now()), func() {
		if r := a.activeRound(); r != nil && r.ID == roundID && r.Phase == phase {
			fn()
		}
	})
}

// armTurnDeadline schedules the automatic advance of the turn in progress. The callback does
// nothing unless that turn of a match in progress is still the current one.
func (a *actor) armTurnDeadline() {
	round := a.state.Round
	turn := round.Turns[round.CurrentTurn]
	if turn.DeadlineAt == nil {
		return
	}
	roundID, turnID := round.ID, turn.ID
	a.arm(&a.phaseTimer, turn.DeadlineAt.Sub(a.m.clock.Now()), func() {
		r := a.activeRound()
		if r == nil || r.ID != roundID || r.Phase != game.PhaseQuestions || r.Turns[r.CurrentTurn].ID != turnID {
			return
		}
		a.advanceTurn(nil, endedByTimeout)
	})
}

// retryLater re-runs a timer-driven transition whose database write failed, after
// storeRetryDelay, as long as the same round of a match in progress is still in the same phase.
// Without it the round would stay in its phase with no deadline left.
func (a *actor) retryLater(fn func()) {
	round := a.activeRound()
	if round == nil {
		return
	}
	roundID, phase := round.ID, round.Phase
	a.arm(&a.phaseTimer, storeRetryDelay, func() {
		if r := a.activeRound(); r != nil && r.ID == roundID && r.Phase == phase {
			fn()
		}
	})
}

// activeRound is the round of the match in progress. It is nil once the match finished or ended
// early: the round of such a match accepts no command, presence change or timer.
func (a *actor) activeRound() *RoundState {
	if a.state.Match.Status != game.MatchInProgress {
		return nil
	}
	return a.state.Round
}

// requireHostInPhase checks that playerID is the host and the round is in phase. It answers c
// with the matching error otherwise.
func (a *actor) requireHostInPhase(playerID string, c Client, phase game.Phase) (*RoundState, bool) {
	if p := a.player(playerID); p == nil || !p.IsHost {
		c.Send(protocol.ErrorMessage(protocol.ErrCodeNotHost))
		return nil, false
	}
	round := a.activeRound()
	if round == nil || round.Phase != phase {
		c.Send(protocol.ErrorMessage(protocol.ErrCodeInvalidPhase))
		return nil, false
	}
	return round, true
}

// requireTurnController checks that the round is in the questions phase and that playerID is
// the answerer of the turn in progress or the host.
func (a *actor) requireTurnController(playerID string, c Client) (*TurnState, bool) {
	round := a.activeRound()
	if round == nil || round.Phase != game.PhaseQuestions {
		c.Send(protocol.ErrorMessage(protocol.ErrCodeInvalidPhase))
		return nil, false
	}
	turn := &round.Turns[round.CurrentTurn]
	if p := a.player(playerID); playerID != turn.AnswererID && (p == nil || !p.IsHost) {
		c.Send(protocol.ErrorMessage(protocol.ErrCodeNotYourTurn))
		return nil, false
	}
	return turn, true
}

// failCommand logs a database failure and tells the player who asked, when there is one.
func (a *actor) failCommand(c Client, action string, err error) {
	a.logError(action, err)
	if c != nil {
		c.Send(protocol.ErrorMessage(protocol.ErrCodeInternal))
	}
}

func (a *actor) turnDuration() time.Duration {
	seconds := a.state.Match.TurnSeconds
	if seconds <= 0 {
		seconds = game.DefaultTurnSeconds
	}
	return time.Duration(seconds) * time.Second
}

// connectedPlayerIDs lists the connected players, oldest first: the players of a new round.
func (a *actor) connectedPlayerIDs() []string {
	var ids []string
	for _, p := range a.state.Players {
		if a.isConnected(p.ID) {
			ids = append(ids, p.ID)
		}
	}
	return ids
}

func (a *actor) theme(id int64) (Theme, bool) {
	for _, t := range a.state.Themes {
		if t.ID == id {
			return t, true
		}
	}
	return Theme{}, false
}

// history lists one field of the earlier rounds of the match, skipping empty values.
func (a *actor) history(field func(PastRound) string) []game.PlayerID {
	var ids []game.PlayerID
	for _, r := range a.state.PastRounds {
		if id := field(r); id != "" {
			ids = append(ids, game.PlayerID(id))
		}
	}
	return ids
}

func toPlayerIDs(ids []string) []game.PlayerID {
	out := make([]game.PlayerID, len(ids))
	for i, id := range ids {
		out[i] = game.PlayerID(id)
	}
	return out
}

// hasPlayer reports whether playerID is in the round's turn ring.
func (r *RoundState) hasPlayer(playerID string) bool {
	for _, turn := range r.Turns {
		if turn.AskerID == playerID {
			return true
		}
	}
	return false
}
