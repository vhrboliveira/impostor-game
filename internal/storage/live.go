package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/uptrace/bun"

	"github.com/vhrboliveira/impostor-game/internal/game"
	"github.com/vhrboliveira/impostor-game/internal/room"
)

// Rooms also implements the persistence of the live rooms (the room actors).
var _ room.LiveStore = (*Rooms)(nil)

// errUnexpectedRows means an update guarded by the expected state changed nothing: the row is
// not where the caller thinks it is (a bug or a manual change, since the room actor is the only
// writer of a live room).
var errUnexpectedRows = errors.New("the row is not in the expected state")

// Authenticate returns the player of the room whose token hashes to tokenHash.
func (r *Rooms) Authenticate(ctx context.Context, code, tokenHash string, now time.Time) (string, error) {
	var found Room
	err := r.db.NewSelect().Model(&found).
		Column("id").
		Where("code = ?", code).
		Where("expires_at > ?", now).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "", room.ErrRoomNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read room: %w", err)
	}

	var player Player
	err = r.db.NewSelect().Model(&player).
		Column("id").
		Where("room_id = ?", found.ID).
		Where("token_hash = ?", tokenHash).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "", room.ErrUnauthorized
	}
	if err != nil {
		return "", fmt.Errorf("authenticate player: %w", err)
	}
	return player.ID, nil
}

// LoadRoom reads the room, its latest match and round (with its theme, secret, impostor and turn
// ring), the rotation history of the match, the players with their scores and the theme catalog,
// all from one consistent snapshot of the database.
func (r *Rooms) LoadRoom(ctx context.Context, code string, now time.Time) (room.RoomState, error) {
	var state room.RoomState

	opts := &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	err := r.db.RunInTx(ctx, opts, func(ctx context.Context, tx bun.Tx) error {
		var rm Room
		err := tx.NewSelect().Model(&rm).
			Where("code = ?", code).
			Where("expires_at > ?", now).
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return room.ErrRoomNotFound
		}
		if err != nil {
			return fmt.Errorf("read room: %w", err)
		}
		state.RoomID, state.Code, state.ExpiresAt, state.EmptySince = rm.ID, rm.Code, rm.ExpiresAt, rm.EmptySince

		var match Match
		if err := tx.NewSelect().Model(&match).
			Where("room_id = ?", rm.ID).
			Order("number DESC").
			Limit(1).
			Scan(ctx); err != nil {
			return fmt.Errorf("read latest match: %w", err)
		}
		state.Match = room.MatchState{
			ID:          match.ID,
			Number:      match.Number,
			Status:      game.MatchStatus(match.Status),
			TotalRounds: match.TotalRounds,
			TurnSeconds: match.TurnSeconds,
		}

		if state.Themes, err = loadThemes(ctx, tx); err != nil {
			return err
		}
		if state.Round, err = loadLatestRound(ctx, tx, match.ID, state.Themes); err != nil {
			return err
		}
		if state.PastRounds, err = loadPastRounds(ctx, tx, match.ID, state.Round); err != nil {
			return err
		}
		state.Players, err = loadPlayers(ctx, tx, rm.ID, match.ID)
		return err
	})
	if err != nil {
		return room.RoomState{}, err
	}
	return state, nil
}

// LoadPlayers re-reads the players of a room with their scores in the given match.
func (r *Rooms) LoadPlayers(ctx context.Context, roomID, matchID string) ([]room.PlayerState, error) {
	return loadPlayers(ctx, r.db, roomID, matchID)
}

// SetEmptySince sets (since non-nil) or clears (since nil) rooms.empty_since.
func (r *Rooms) SetEmptySince(ctx context.Context, roomID string, since *time.Time) error {
	if _, err := r.db.NewUpdate().Model((*Room)(nil)).
		Set("empty_since = ?", since).
		Where("id = ?", roomID).
		Exec(ctx); err != nil {
		return fmt.Errorf("update empty_since: %w", err)
	}
	return nil
}

// TransferHost makes playerID the only host of the room, in one transaction (the partial unique
// index allows a single host per room at any time).
func (r *Rooms) TransferHost(ctx context.Context, roomID, playerID string) error {
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewUpdate().Model((*Player)(nil)).
			Set("is_host = false").
			Where("room_id = ?", roomID).
			Where("is_host").
			Where("id <> ?", playerID).
			Exec(ctx); err != nil {
			return fmt.Errorf("clear host: %w", err)
		}

		res, err := tx.NewUpdate().Model((*Player)(nil)).
			Set("is_host = true").
			Where("room_id = ?", roomID).
			Where("id = ?", playerID).
			Exec(ctx)
		return expectOneRow(res, err, "set host")
	})
}

// StartMatch sets the match in progress and creates round 1 in theme selection. The room row is
// locked first, like in AddPlayer, so a join and a start never interleave; the players are read
// again inside the transaction.
func (r *Rooms) StartMatch(ctx context.Context, params room.StartMatchParams) (room.StartedMatch, error) {
	var started room.StartedMatch

	err := r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var locked Room
		if err := tx.NewSelect().Model(&locked).
			Column("id").
			Where("id = ?", params.RoomID).
			For("UPDATE").
			Scan(ctx); err != nil {
			return fmt.Errorf("lock room: %w", err)
		}

		res, err := tx.NewUpdate().Model((*Match)(nil)).
			Set("status = ?", string(game.MatchInProgress)).
			Set("started_at = ?", params.Now).
			Where("id = ?", params.MatchID).
			Where("room_id = ?", params.RoomID).
			Where("status = ?", string(game.MatchLobby)).
			Exec(ctx)
		if err := expectOneRow(res, err, "start match"); err != nil {
			return err
		}

		first := &Round{
			MatchID:   params.MatchID,
			Number:    1,
			Phase:     string(game.PhaseThemeSelection),
			CreatedAt: params.Now,
		}
		if _, err := tx.NewInsert().Model(first).Returning("id").Exec(ctx); err != nil {
			return fmt.Errorf("create first round: %w", err)
		}
		started.Round = room.RoundState{ID: first.ID, Number: 1, Phase: game.PhaseThemeSelection}

		started.Players, err = loadPlayers(ctx, tx, params.RoomID, params.MatchID)
		return err
	})
	if err != nil {
		return room.StartedMatch{}, err
	}
	return started, nil
}

// LoadRoundCandidates returns the words of the theme never used in any round of the room (none
// for a players theme) and the questions of the theme plus the generic ones of its kind.
func (r *Rooms) LoadRoundCandidates(ctx context.Context, roomID string, theme room.Theme) (room.RoundCandidates, error) {
	var candidates room.RoundCandidates

	if theme.Kind == game.ThemeKindWords {
		usedInRoom := r.db.NewSelect().Model((*Round)(nil)).
			ColumnExpr("1").
			Join("JOIN matches AS m ON m.id = rd.match_id").
			Where("m.room_id = ?", roomID).
			Where("rd.word_id = w.id")

		var words []Word
		if err := r.db.NewSelect().Model(&words).
			Where("w.theme_id = ?", theme.ID).
			Where("NOT EXISTS (?)", usedInRoom).
			Order("w.id").
			Scan(ctx); err != nil {
			return room.RoundCandidates{}, fmt.Errorf("read unused words: %w", err)
		}
		for _, w := range words {
			candidates.Words = append(candidates.Words, room.Word{ID: w.ID, Text: w.Text})
		}
	}

	var questions []Question
	if err := r.db.NewSelect().Model(&questions).
		Where("kind = ?", string(theme.Kind)).
		WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where("theme_id = ?", theme.ID).WhereOr("theme_id IS NULL")
		}).
		Order("id").
		Scan(ctx); err != nil {
		return room.RoundCandidates{}, fmt.Errorf("read questions: %w", err)
	}
	for _, q := range questions {
		candidates.Questions = append(candidates.Questions, room.Question{ID: q.ID, Text: q.Text})
	}
	return candidates, nil
}

// BeginRound records the theme, the secret, the impostor and the turn ring, and moves the round
// from the theme selection to the reveal.
func (r *Rooms) BeginRound(ctx context.Context, params room.BeginRoundParams) ([]string, error) {
	var turnIDs []string

	err := r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewUpdate().Model((*Round)(nil)).
			Set("phase = ?", string(game.PhaseReveal)).
			Set("theme_id = ?", params.ThemeID).
			Set("theme_drawn = ?", params.ThemeDrawn).
			Set("word_id = ?", optionalInt64(params.WordID)).
			Set("subject_player_id = ?", optionalString(params.SubjectID)).
			Set("impostor_player_id = ?", params.ImpostorID).
			Set("current_turn = 0").
			Where("id = ?", params.RoundID).
			Where("phase = ?", string(game.PhaseThemeSelection)).
			Exec(ctx)
		if err := expectOneRow(res, err, "begin round"); err != nil {
			return err
		}

		turns := make([]Turn, len(params.Turns))
		for position, planned := range params.Turns {
			turns[position] = Turn{
				RoundID:    params.RoundID,
				Position:   position,
				AskerID:    planned.AskerID,
				AnswererID: planned.AnswererID,
				QuestionID: planned.QuestionID,
			}
		}
		if _, err := tx.NewInsert().Model(&turns).Exec(ctx); err != nil {
			return fmt.Errorf("insert turns: %w", err)
		}

		// Read the ids back by position instead of trusting the order of RETURNING.
		var inserted []Turn
		if err := tx.NewSelect().Model(&inserted).
			Column("id").
			Where("round_id = ?", params.RoundID).
			Order("position").
			Scan(ctx); err != nil {
			return fmt.Errorf("read turn ids: %w", err)
		}
		for _, t := range inserted {
			turnIDs = append(turnIDs, t.ID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return turnIDs, nil
}

// StartQuestions moves the round to the questions phase and starts its first turn.
func (r *Rooms) StartQuestions(ctx context.Context, roundID, turnID string, now, deadline time.Time) error {
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewUpdate().Model((*Round)(nil)).
			Set("phase = ?", string(game.PhaseQuestions)).
			Set("current_turn = 0").
			Where("id = ?", roundID).
			Where("phase = ?", string(game.PhaseReveal)).
			Exec(ctx)
		if err := expectOneRow(res, err, "start questions"); err != nil {
			return err
		}
		return startTurn(ctx, tx, roundID, turnID, now, deadline)
	})
}

// ExtendTurn moves the deadline of a turn that has not ended.
func (r *Rooms) ExtendTurn(ctx context.Context, turnID string, deadline time.Time) error {
	res, err := r.db.NewUpdate().Model((*Turn)(nil)).
		Set("deadline_at = ?", deadline).
		Where("id = ?", turnID).
		Where("ended_at IS NULL").
		Exec(ctx)
	return expectOneRow(res, err, "extend turn")
}

// AdvanceTurn ends the turn in progress and starts the next one, or the discussion after the
// last turn.
func (r *Rooms) AdvanceTurn(ctx context.Context, params room.AdvanceTurnParams) error {
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewUpdate().Model((*Turn)(nil)).
			Set("ended_at = ?", params.Now).
			Set("ended_by = ?", params.EndedBy).
			Where("id = ?", params.EndedTurnID).
			Where("round_id = ?", params.RoundID).
			Where("ended_at IS NULL").
			Exec(ctx)
		if err := expectOneRow(res, err, "end turn"); err != nil {
			return err
		}

		if params.NextTurnID == "" {
			res, err = tx.NewUpdate().Model((*Round)(nil)).
				Set("phase = ?", string(game.PhaseDiscussion)).
				Set("phase_deadline_at = ?", params.DiscussionDeadline).
				Where("id = ?", params.RoundID).
				Where("phase = ?", string(game.PhaseQuestions)).
				Exec(ctx)
			return expectOneRow(res, err, "start discussion")
		}

		res, err = tx.NewUpdate().Model((*Round)(nil)).
			Set("current_turn = ?", params.NextPosition).
			Where("id = ?", params.RoundID).
			Where("phase = ?", string(game.PhaseQuestions)).
			Exec(ctx)
		if err := expectOneRow(res, err, "move current turn"); err != nil {
			return err
		}
		return startTurn(ctx, tx, params.RoundID, params.NextTurnID, params.Now, params.NextDeadline)
	})
}

// ExtendDiscussion moves the end of a round that is in the discussion.
func (r *Rooms) ExtendDiscussion(ctx context.Context, roundID string, deadline time.Time) error {
	res, err := r.db.NewUpdate().Model((*Round)(nil)).
		Set("phase_deadline_at = ?", deadline).
		Where("id = ?", roundID).
		Where("phase = ?", string(game.PhaseDiscussion)).
		Exec(ctx)
	return expectOneRow(res, err, "extend discussion")
}

// StartVoting moves the round from the discussion to the voting, which ends at deadline.
func (r *Rooms) StartVoting(ctx context.Context, roundID string, deadline time.Time) error {
	res, err := r.db.NewUpdate().Model((*Round)(nil)).
		Set("phase = ?", string(game.PhaseVoting)).
		Set("phase_deadline_at = ?", deadline).
		Where("id = ?", roundID).
		Where("phase = ?", string(game.PhaseDiscussion)).
		Exec(ctx)
	return expectOneRow(res, err, "start voting")
}

// CastVote records a vote. The primary key (round, voter) makes votes final.
func (r *Rooms) CastVote(ctx context.Context, roundID, voterID, targetID string, now time.Time) error {
	vote := &Vote{RoundID: roundID, VoterID: voterID, TargetID: &targetID, CastAt: now}
	if _, err := r.db.NewInsert().Model(vote).Exec(ctx); err != nil {
		return fmt.Errorf("cast vote: %w", err)
	}
	return nil
}

// LoadThemeWords returns every word of a theme, ordered by id.
func (r *Rooms) LoadThemeWords(ctx context.Context, themeID int64) ([]room.Word, error) {
	var rows []Word
	if err := r.db.NewSelect().Model(&rows).Where("theme_id = ?", themeID).Order("id").Scan(ctx); err != nil {
		return nil, fmt.Errorf("read theme words: %w", err)
	}
	words := make([]room.Word, 0, len(rows))
	for _, w := range rows {
		words = append(words, room.Word{ID: w.ID, Text: w.Text})
	}
	return words, nil
}

// StartGuess records the abstentions, marks the impostor found and moves the voting to the
// guess with its options (a JSON array of ids) and deadline.
func (r *Rooms) StartGuess(ctx context.Context, params room.StartGuessParams) error {
	options, err := json.Marshal(params.OptionIDs)
	if err != nil {
		return fmt.Errorf("encode guess options: %w", err)
	}
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := insertAbstentions(ctx, tx, params.RoundID, params.Abstainers); err != nil {
			return err
		}
		res, err := tx.NewUpdate().Model((*Round)(nil)).
			Set("phase = ?", string(game.PhaseGuess)).
			Set("impostor_found = true").
			Set("guess_options = ?", string(options)).
			Set("phase_deadline_at = ?", params.Deadline).
			Where("id = ?", params.RoundID).
			Where("phase = ?", string(game.PhaseVoting)).
			Exec(ctx)
		return expectOneRow(res, err, "start guess")
	})
}

// FinishRound records the abstentions (when coming from the voting), the outcome, the guess and
// the points, and moves the round to the result.
func (r *Rooms) FinishRound(ctx context.Context, params room.FinishRoundParams) error {
	var correct *bool
	if params.ImpostorFound {
		correct = &params.GuessCorrect
	}
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := insertAbstentions(ctx, tx, params.RoundID, params.Abstainers); err != nil {
			return err
		}
		res, err := tx.NewUpdate().Model((*Round)(nil)).
			Set("phase = ?", string(game.PhaseResult)).
			Set("impostor_found = ?", params.ImpostorFound).
			Set("guess_word_id = ?", optionalInt64(params.GuessWordID)).
			Set("guess_player_id = ?", optionalString(params.GuessPlayerID)).
			Set("guess_correct = ?", correct).
			Set("phase_deadline_at = NULL").
			Where("id = ?", params.RoundID).
			Where("phase IN (?)", bun.List([]string{string(game.PhaseVoting), string(game.PhaseGuess)})).
			Exec(ctx)
		if err := expectOneRow(res, err, "finish round"); err != nil {
			return err
		}

		if len(params.Scores) == 0 {
			return nil
		}
		scores := make([]RoundScore, len(params.Scores))
		for i, s := range params.Scores {
			scores[i] = RoundScore{RoundID: params.RoundID, PlayerID: string(s.Player), Reason: string(s.Reason), Points: s.Points}
		}
		if _, err := tx.NewInsert().Model(&scores).Exec(ctx); err != nil {
			return fmt.Errorf("insert round scores: %w", err)
		}
		return nil
	})
}

// NextRound creates round number of the match in theme selection.
func (r *Rooms) NextRound(ctx context.Context, matchID string, number int, now time.Time) (string, error) {
	round := &Round{MatchID: matchID, Number: number, Phase: string(game.PhaseThemeSelection), CreatedAt: now}
	if _, err := r.db.NewInsert().Model(round).Returning("id").Exec(ctx); err != nil {
		return "", fmt.Errorf("create round %d: %w", number, err)
	}
	return round.ID, nil
}

// EndMatch closes a match in progress as finished or ended early.
func (r *Rooms) EndMatch(ctx context.Context, matchID string, status game.MatchStatus, now time.Time) error {
	res, err := r.db.NewUpdate().Model((*Match)(nil)).
		Set("status = ?", string(status)).
		Set("ended_at = ?", now).
		Where("id = ?", matchID).
		Where("status = ?", string(game.MatchInProgress)).
		Exec(ctx)
	return expectOneRow(res, err, "end match")
}

// PlayAgain opens a new match in the lobby of the room.
func (r *Rooms) PlayAgain(ctx context.Context, params room.PlayAgainParams) (room.MatchState, error) {
	match := &Match{
		RoomID:      params.RoomID,
		Number:      params.Number,
		Status:      string(game.MatchLobby),
		TotalRounds: params.TotalRounds,
		TurnSeconds: params.TurnSeconds,
		CreatedAt:   params.Now,
	}
	if _, err := r.db.NewInsert().Model(match).Returning("id").Exec(ctx); err != nil {
		return room.MatchState{}, fmt.Errorf("create match %d: %w", params.Number, err)
	}
	return room.MatchState{
		ID:          match.ID,
		Number:      match.Number,
		Status:      game.MatchLobby,
		TotalRounds: match.TotalRounds,
		TurnSeconds: match.TurnSeconds,
	}, nil
}

// insertAbstentions records the players who did not vote, with a NULL target.
func insertAbstentions(ctx context.Context, tx bun.Tx, roundID string, abstainers []string) error {
	if len(abstainers) == 0 {
		return nil
	}
	votes := make([]Vote, len(abstainers))
	for i, id := range abstainers {
		votes[i] = Vote{RoundID: roundID, VoterID: id}
	}
	if _, err := tx.NewInsert().Model(&votes).Exec(ctx); err != nil {
		return fmt.Errorf("record abstentions: %w", err)
	}
	return nil
}

// startTurn records when a turn started and its deadline.
func startTurn(ctx context.Context, tx bun.Tx, roundID, turnID string, now, deadline time.Time) error {
	res, err := tx.NewUpdate().Model((*Turn)(nil)).
		Set("started_at = ?", now).
		Set("deadline_at = ?", deadline).
		Where("id = ?", turnID).
		Where("round_id = ?", roundID).
		Exec(ctx)
	return expectOneRow(res, err, "start turn")
}

// playerWithScore is a player row plus the sum of their points in a match.
type playerWithScore struct {
	Player `bun:",extend"`

	Score    int        `bun:"score"`
	AvatarAt *time.Time `bun:"avatar_at"`
}

// loadPlayers returns the players of a room, oldest first, with their points in the match and
// the version of their photo (never the image itself).
func loadPlayers(ctx context.Context, db bun.IDB, roomID, matchID string) ([]room.PlayerState, error) {
	pointsInMatch := db.NewSelect().Model((*RoundScore)(nil)).
		ColumnExpr("rs.player_id").
		ColumnExpr("sum(rs.points) AS points").
		Join("JOIN rounds AS rd ON rd.id = rs.round_id").
		Where("rd.match_id = ?", matchID).
		Group("rs.player_id")

	var rows []playerWithScore
	if err := db.NewSelect().Model(&rows).
		ColumnExpr("p.*").
		ColumnExpr("COALESCE(s.points, 0) AS score").
		ColumnExpr("pa.updated_at AS avatar_at").
		Join("LEFT JOIN (?) AS s ON s.player_id = p.id", pointsInMatch).
		Join("LEFT JOIN player_avatars AS pa ON pa.player_id = p.id").
		Where("p.room_id = ?", roomID).
		Order("p.joined_at", "p.id").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("read players: %w", err)
	}

	players := make([]room.PlayerState, 0, len(rows))
	for _, row := range rows {
		players = append(players, room.PlayerState{
			ID:       row.ID,
			Name:     row.Name,
			IsHost:   row.IsHost,
			JoinedAt: row.JoinedAt,
			Score:    row.Score,
			AvatarAt: row.AvatarAt,
		})
	}
	return players, nil
}

// loadThemes returns the theme catalog ordered by name.
func loadThemes(ctx context.Context, db bun.IDB) ([]room.Theme, error) {
	var rows []Theme
	if err := db.NewSelect().Model(&rows).Order("name", "id").Scan(ctx); err != nil {
		return nil, fmt.Errorf("read themes: %w", err)
	}

	themes := make([]room.Theme, 0, len(rows))
	for _, row := range rows {
		themes = append(themes, room.Theme{ID: row.ID, Name: row.Name, Kind: game.ThemeKind(row.Kind)})
	}
	return themes, nil
}

// loadLatestRound returns the latest round of the match with its turn ring, or nil when the
// match has no round yet. The secret is the word's text or the subject's name.
func loadLatestRound(ctx context.Context, db bun.IDB, matchID string, themes []room.Theme) (*room.RoundState, error) {
	var row Round
	err := db.NewSelect().Model(&row).
		Relation("Word").
		Relation("Subject").
		Where("rd.match_id = ?", matchID).
		Order("rd.number DESC").
		Limit(1).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read latest round: %w", err)
	}

	round := &room.RoundState{
		ID:            row.ID,
		Number:        row.Number,
		Phase:         game.Phase(row.Phase),
		ThemeDrawn:    row.ThemeDrawn,
		ImpostorID:    deref(row.ImpostorPlayerID),
		SubjectID:     deref(row.SubjectPlayerID),
		CurrentTurn:   row.CurrentTurn,
		PhaseDeadline: row.PhaseDeadlineAt,
		ImpostorFound: row.ImpostorFound != nil && *row.ImpostorFound,
	}
	if row.WordID != nil {
		round.WordID = *row.WordID
	}
	switch {
	case row.Word != nil:
		round.Secret = row.Word.Text
	case row.Subject != nil:
		round.Secret = row.Subject.Name
	}
	if row.ThemeID != nil {
		for i := range themes {
			if themes[i].ID == *row.ThemeID {
				theme := themes[i]
				round.Theme = &theme
			}
		}
	}

	var turns []Turn
	if err := db.NewSelect().Model(&turns).
		Relation("Question").
		Where("tn.round_id = ?", round.ID).
		Order("tn.position").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("read turns: %w", err)
	}
	for _, t := range turns {
		turn := room.TurnState{
			ID:         t.ID,
			Position:   t.Position,
			AskerID:    t.AskerID,
			AnswererID: t.AnswererID,
			DeadlineAt: t.DeadlineAt,
		}
		if t.Question != nil {
			turn.Question = t.Question.Text
		}
		round.Turns = append(round.Turns, turn)
	}

	if err := loadRoundOutcome(ctx, db, round, row); err != nil {
		return nil, err
	}
	return round, nil
}

// loadRoundOutcome fills what the voting, the guess and the result recorded: the votes, the
// guess options with their labels, the guess and the points of the round.
func loadRoundOutcome(ctx context.Context, db bun.IDB, round *room.RoundState, row Round) error {
	var votes []Vote
	if err := db.NewSelect().Model(&votes).Where("round_id = ?", round.ID).Scan(ctx); err != nil {
		return fmt.Errorf("read votes: %w", err)
	}
	if len(votes) > 0 {
		round.Votes = make(map[string]string, len(votes))
		for _, v := range votes {
			round.Votes[v.VoterID] = deref(v.TargetID)
		}
	}

	if len(row.GuessOptions) > 0 {
		var ids []string
		if err := json.Unmarshal(row.GuessOptions, &ids); err != nil {
			return fmt.Errorf("decode guess options: %w", err)
		}
		labels, err := guessLabels(ctx, db, round, ids)
		if err != nil {
			return err
		}
		for _, id := range ids {
			round.GuessOptions = append(round.GuessOptions, room.GuessOption{ID: id, Label: labels[id]})
		}
	}

	if round.Phase == game.PhaseResult && round.ImpostorFound {
		guess := &room.GuessState{Correct: row.GuessCorrect != nil && *row.GuessCorrect}
		switch {
		case row.GuessWordID != nil:
			guess.OptionID = strconv.FormatInt(*row.GuessWordID, 10)
		case row.GuessPlayerID != nil:
			guess.OptionID = *row.GuessPlayerID
		}
		round.Guess = guess
	}

	var scores []RoundScore
	if err := db.NewSelect().Model(&scores).Where("round_id = ?", round.ID).Scan(ctx); err != nil {
		return fmt.Errorf("read round scores: %w", err)
	}
	if len(scores) > 0 {
		round.Points = make(map[string]int)
		for _, s := range scores {
			round.Points[s.PlayerID] += s.Points
		}
	}
	return nil
}

// guessLabels maps guess option ids to what the impostor reads: word texts, or player names in
// a players theme.
func guessLabels(ctx context.Context, db bun.IDB, round *room.RoundState, ids []string) (map[string]string, error) {
	labels := make(map[string]string, len(ids))
	if round.Theme != nil && round.Theme.Kind == game.ThemeKindPlayers {
		var players []Player
		if err := db.NewSelect().Model(&players).Column("id", "name").Where("id IN (?)", bun.List(ids)).Scan(ctx); err != nil {
			return nil, fmt.Errorf("read guess players: %w", err)
		}
		for _, p := range players {
			labels[p.ID] = p.Name
		}
		return labels, nil
	}

	wordIDs := make([]int64, 0, len(ids))
	for _, id := range ids {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("guess option %q is not a word id: %w", id, err)
		}
		wordIDs = append(wordIDs, n)
	}
	var words []Word
	if err := db.NewSelect().Model(&words).Column("id", "text").Where("id IN (?)", bun.List(wordIDs)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("read guess words: %w", err)
	}
	for _, w := range words {
		labels[strconv.FormatInt(w.ID, 10)] = w.Text
	}
	return labels, nil
}

// loadPastRounds returns who was the impostor (and the subject) in the earlier rounds of the
// match, oldest first.
func loadPastRounds(ctx context.Context, db bun.IDB, matchID string, latest *room.RoundState) ([]room.PastRound, error) {
	q := db.NewSelect().Model((*Round)(nil)).
		Column("impostor_player_id", "subject_player_id").
		Where("match_id = ?", matchID).
		Where("impostor_player_id IS NOT NULL").
		Order("number")
	if latest != nil {
		q = q.Where("number < ?", latest.Number)
	}

	var rows []Round
	if err := q.Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("read past rounds: %w", err)
	}

	past := make([]room.PastRound, 0, len(rows))
	for _, row := range rows {
		past = append(past, room.PastRound{ImpostorID: deref(row.ImpostorPlayerID), SubjectID: deref(row.SubjectPlayerID)})
	}
	return past, nil
}

func expectOneRow(res sql.Result, err error, action string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("%s: %w", action, errUnexpectedRows)
	}
	return nil
}

// optionalInt64 maps the zero value to NULL.
func optionalInt64(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

// optionalString maps the empty string to NULL.
func optionalString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
