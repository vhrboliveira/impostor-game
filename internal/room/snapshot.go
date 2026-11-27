package room

import (
	"github.com/vhrboliveira/impostor-game/internal/game"
	"github.com/vhrboliveira/impostor-game/internal/protocol"
)

// snapshotFor builds the state of the room as playerID may see it. Every snapshot gets fresh
// slices, so the connection that encodes it never shares memory with the actor.
//
// Security rules: the secret only goes to the players of the round who are not the impostor
// (everyone sees it in the result); nobody learns who the impostor is before the result; during
// the questions only the turns up to the current one are sent; votes stay secret until the
// result (only who already voted is shown).
func (a *actor) snapshotFor(playerID string) protocol.RoomSnapshot {
	players := make([]protocol.PlayerView, 0, len(a.state.Players))
	for _, p := range a.state.Players {
		var avatarURL *string
		if p.AvatarAt != nil {
			url := AvatarURL(a.state.Code, p.ID, *p.AvatarAt)
			avatarURL = &url
		}
		players = append(players, protocol.PlayerView{
			ID:          p.ID,
			Name:        p.Name,
			IsHost:      p.IsHost,
			IsConnected: a.isConnected(p.ID),
			Score:       p.Score,
			AvatarURL:   avatarURL,
			HasLeft:     !p.Active(),
		})
	}

	var (
		round       *protocol.RoundView
		roundNumber int
	)
	if r := a.state.Round; r != nil {
		roundNumber = r.Number
		round = a.roundView(r, playerID)
	}

	return protocol.RoomSnapshot{
		RoomCode: a.state.Code,
		Match: protocol.MatchView{
			Status:      string(a.state.Match.Status),
			TotalRounds: a.state.Match.TotalRounds,
			RoundNumber: roundNumber,
		},
		Players: players,
		Round:   round,
		Me:      a.meView(playerID),
	}
}

func (a *actor) roundView(r *RoundState, playerID string) *protocol.RoundView {
	view := &protocol.RoundView{
		Number:       r.Number,
		Phase:        string(r.Phase),
		ThemeOptions: []protocol.ThemeOption{},
		TotalTurns:   len(r.Turns),
		ConfirmedIDs: []string{},
		VotedIDs:     []string{},
		GuessOptions: []protocol.GuessOption{},
		Turns:        []protocol.TurnView{},
	}
	if r.Theme != nil {
		name := r.Theme.Name
		view.ThemeName = &name
	}

	switch r.Phase {
	case game.PhaseThemeSelection:
		for _, t := range a.state.Themes {
			view.ThemeOptions = append(view.ThemeOptions, protocol.ThemeOption{ID: t.ID, Name: t.Name})
		}
	case game.PhaseReveal:
		// Player order, not ring order: the ring (who asks whom) is only shown turn by turn.
		for _, p := range a.state.Players {
			if a.confirmed[p.ID] && r.hasPlayer(p.ID) {
				view.ConfirmedIDs = append(view.ConfirmedIDs, p.ID)
			}
		}
	case game.PhaseQuestions:
		view.Turns = turnViews(r.Turns[:r.CurrentTurn+1], r.CurrentTurn)
	case game.PhaseDiscussion, game.PhaseVoting, game.PhaseGuess, game.PhaseResult:
		view.Turns = turnViews(r.Turns, -1)
		if r.PhaseDeadline != nil && r.Phase != game.PhaseResult {
			deadline := *r.PhaseDeadline
			view.PhaseDeadlineAt = &deadline
		}
	}

	switch r.Phase {
	case game.PhaseVoting:
		for _, id := range r.playerIDs() {
			if r.Votes[id] != "" {
				view.VotedIDs = append(view.VotedIDs, id)
			}
		}
	case game.PhaseGuess:
		// Only the found impostor picks. In a players theme the options leave the impostor out, so
		// sending them to anyone else would reveal who the impostor is before the result.
		if playerID == r.ImpostorID {
			for _, o := range r.GuessOptions {
				view.GuessOptions = append(view.GuessOptions, protocol.GuessOption{ID: o.ID, Label: o.Label})
			}
		}
	case game.PhaseResult:
		view.Result = resultView(r)
	}
	return view
}

// resultView reveals the round: the impostor, the secret, every vote, the guess and the points.
func resultView(r *RoundState) *protocol.RoundResult {
	result := &protocol.RoundResult{
		ImpostorID:    r.ImpostorID,
		ImpostorFound: r.ImpostorFound,
		Secret:        r.Secret,
		Votes:         []protocol.VoteView{},
		Points:        []protocol.PointsView{},
	}
	for _, id := range r.playerIDs() {
		vote := protocol.VoteView{VoterID: id}
		if target := r.Votes[id]; target != "" {
			vote.TargetID = &target
		}
		result.Votes = append(result.Votes, vote)
		if points := r.Points[id]; points > 0 {
			result.Points = append(result.Points, protocol.PointsView{PlayerID: id, Points: points})
		}
	}
	if r.ImpostorFound && r.Guess != nil {
		guess := &protocol.GuessView{Correct: r.Guess.Correct}
		for _, o := range r.GuessOptions {
			if o.ID == r.Guess.OptionID {
				label := o.Label
				guess.Label = &label
			}
		}
		result.Guess = guess
	}
	return result
}

// turnViews converts turns; only the turn at position current carries its deadline.
func turnViews(turns []TurnState, current int) []protocol.TurnView {
	views := make([]protocol.TurnView, 0, len(turns))
	for _, t := range turns {
		view := protocol.TurnView{
			Position:   t.Position,
			AskerID:    t.AskerID,
			AnswererID: t.AnswererID,
			Question:   t.Question,
		}
		if t.Position == current && t.DeadlineAt != nil {
			deadline := *t.DeadlineAt
			view.DeadlineAt = &deadline
		}
		views = append(views, view)
	}
	return views
}

// meView is what only playerID sees: their role and, unless they are the impostor, the secret.
// Players outside the round (offline when it started) get neither.
func (a *actor) meView(playerID string) protocol.MeView {
	me := protocol.MeView{PlayerID: playerID}
	r := a.state.Round
	if r == nil || r.Phase == game.PhaseThemeSelection || !r.hasPlayer(playerID) {
		return me
	}

	role := protocol.RoleInsider
	if playerID == r.ImpostorID {
		role = protocol.RoleImpostor
	} else {
		secret := r.Secret
		me.Secret = &secret
	}
	me.Role = &role
	if target := r.Votes[playerID]; target != "" {
		me.VoteTargetID = &target
	}
	return me
}
