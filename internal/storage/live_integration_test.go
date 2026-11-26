//go:build integration

package storage_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"

	"github.com/vhrboliveira/impostor-game/internal/game"
	"github.com/vhrboliveira/impostor-game/internal/room"
	"github.com/vhrboliveira/impostor-game/internal/storage"
)

// liveRoom is room ABCDE with host Ana (token hash "host-hash") and Bia ("hash-Bia").
type liveRoom struct {
	db     *bun.DB
	rooms  *storage.Rooms
	roomID string
	ana    string
	bia    string
}

func newLiveRoom(t *testing.T) liveRoom {
	t.Helper()
	ctx := context.Background()
	db := newMigratedDB(t)
	rooms := storage.NewRooms(db)

	host, err := rooms.CreateRoom(ctx, createParams("ABCDE"))
	require.NoError(t, err)
	bia, err := rooms.AddPlayer(ctx, addParams("ABCDE", "Bia"))
	require.NoError(t, err)

	return liveRoom{db: db, rooms: rooms, roomID: host.RoomID, ana: host.ID, bia: bia.ID}
}

func TestRooms_Authenticate(t *testing.T) {
	ctx := context.Background()
	lr := newLiveRoom(t)

	t.Run("a player's token", func(t *testing.T) {
		id, err := lr.rooms.Authenticate(ctx, "ABCDE", "hash-Bia", lobbyNow)

		require.NoError(t, err)
		assert.Equal(t, lr.bia, id)
	})

	t.Run("a token of no player", func(t *testing.T) {
		_, err := lr.rooms.Authenticate(ctx, "ABCDE", "unknown", lobbyNow)

		require.ErrorIs(t, err, room.ErrUnauthorized)
	})

	t.Run("an unknown room", func(t *testing.T) {
		_, err := lr.rooms.Authenticate(ctx, "ZZZZZ", "hash-Bia", lobbyNow)

		require.ErrorIs(t, err, room.ErrRoomNotFound)
	})

	t.Run("an expired room", func(t *testing.T) {
		_, err := lr.rooms.Authenticate(ctx, "ABCDE", "hash-Bia", lobbyNow.Add(25*time.Hour))

		require.ErrorIs(t, err, room.ErrRoomNotFound)
	})
}

func TestRooms_LoadRoom(t *testing.T) {
	ctx := context.Background()

	t.Run("a room in the lobby", func(t *testing.T) {
		lr := newLiveRoom(t)
		insert(t, lr.db, &[]storage.Theme{
			{Slug: "p", Name: "Pessoas da sala", Kind: "players"},
			{Slug: "f", Name: "Comidas", Kind: "words"},
		})

		state, err := lr.rooms.LoadRoom(ctx, "ABCDE", lobbyNow)

		require.NoError(t, err)
		assert.Equal(t, lr.roomID, state.RoomID)
		assert.Equal(t, "ABCDE", state.Code)
		assert.True(t, state.ExpiresAt.Equal(lobbyNow.Add(24*time.Hour)))
		require.NotNil(t, state.EmptySince)
		assert.True(t, state.EmptySince.Equal(lobbyNow))

		assert.NotEmpty(t, state.Match.ID)
		assert.Equal(t, 1, state.Match.Number)
		assert.Equal(t, game.MatchLobby, state.Match.Status)
		assert.Equal(t, 5, state.Match.TotalRounds)
		assert.Nil(t, state.Round)

		require.Len(t, state.Players, 2)
		assert.Equal(t, room.PlayerState{ID: lr.ana, Name: "Ana", IsHost: true, JoinedAt: state.Players[0].JoinedAt}, state.Players[0])
		assert.Equal(t, lr.bia, state.Players[1].ID, "players are ordered by join time")
		assert.False(t, state.Players[1].IsHost)

		require.Len(t, state.Themes, 2)
		assert.Equal(t, "Comidas", state.Themes[0].Name, "themes are ordered by name")
		assert.Equal(t, game.ThemeKindWords, state.Themes[0].Kind)
		assert.Equal(t, "Pessoas da sala", state.Themes[1].Name)
		assert.Equal(t, game.ThemeKindPlayers, state.Themes[1].Kind)
		assert.Positive(t, state.Themes[0].ID)
		assert.Equal(t, 180, state.Match.TurnSeconds)
		assert.Empty(t, state.PastRounds)
	})

	t.Run("the latest round and the scores of the match", func(t *testing.T) {
		lr := newLiveRoom(t)
		started, err := lr.rooms.StartMatch(ctx, room.StartMatchParams{RoomID: lr.roomID, MatchID: matchID(t, lr), Now: lobbyNow})
		require.NoError(t, err)
		roundID := started.Round.ID
		insert(t, lr.db, &[]storage.RoundScore{
			{RoundID: roundID, PlayerID: lr.bia, Reason: "correct_vote", Points: 1},
			{RoundID: roundID, PlayerID: lr.ana, Reason: "impostor_escaped", Points: 2},
		})
		exec(t, lr.db.NewUpdate().Model((*storage.Round)(nil)).Set("phase = 'voting'").Where("id = ?", roundID))

		state, err := lr.rooms.LoadRoom(ctx, "ABCDE", lobbyNow)

		require.NoError(t, err)
		assert.Equal(t, game.MatchInProgress, state.Match.Status)
		require.NotNil(t, state.Round)
		assert.Equal(t, room.RoundState{
			ID: roundID, Number: 1, Phase: game.PhaseVoting,
			Points: map[string]int{lr.ana: 2, lr.bia: 1},
		}, *state.Round)
		assert.Equal(t, 2, state.Players[0].Score)
		assert.Equal(t, 1, state.Players[1].Score)
		assert.Empty(t, state.Themes)
	})

	t.Run("unknown and expired rooms", func(t *testing.T) {
		lr := newLiveRoom(t)

		_, err := lr.rooms.LoadRoom(ctx, "ZZZZZ", lobbyNow)
		require.ErrorIs(t, err, room.ErrRoomNotFound)

		_, err = lr.rooms.LoadRoom(ctx, "ABCDE", lobbyNow.Add(24*time.Hour))
		require.ErrorIs(t, err, room.ErrRoomNotFound)
	})
}

func matchID(t *testing.T, lr liveRoom) string {
	t.Helper()
	return latestMatch(t, lr.db, lr.roomID).ID
}

func TestRooms_SetEmptySince(t *testing.T) {
	ctx := context.Background()
	lr := newLiveRoom(t)
	readEmptySince := func() *time.Time {
		var r storage.Room
		require.NoError(t, lr.db.NewSelect().Model(&r).Where("id = ?", lr.roomID).Scan(ctx))
		return r.EmptySince
	}

	require.NoError(t, lr.rooms.SetEmptySince(ctx, lr.roomID, nil))
	assert.Nil(t, readEmptySince())

	later := lobbyNow.Add(time.Hour)
	require.NoError(t, lr.rooms.SetEmptySince(ctx, lr.roomID, &later))
	got := readEmptySince()
	require.NotNil(t, got)
	assert.True(t, got.Equal(later))
}

func TestRooms_TransferHost(t *testing.T) {
	ctx := context.Background()
	lr := newLiveRoom(t)

	require.NoError(t, lr.rooms.TransferHost(ctx, lr.roomID, lr.bia))

	players, err := lr.rooms.LoadPlayers(ctx, lr.roomID, matchID(t, lr))
	require.NoError(t, err)
	assert.False(t, players[0].IsHost)
	assert.True(t, players[1].IsHost)

	err = lr.rooms.TransferHost(ctx, lr.roomID, "00000000-0000-0000-0000-000000000000")
	require.Error(t, err, "a player outside the room cannot become host")
	players, err = lr.rooms.LoadPlayers(ctx, lr.roomID, matchID(t, lr))
	require.NoError(t, err)
	assert.True(t, players[1].IsHost, "the failed transfer rolled back")
}

func TestRooms_StartMatch(t *testing.T) {
	ctx := context.Background()

	t.Run("sets the match in progress and creates round 1 in theme selection", func(t *testing.T) {
		lr := newLiveRoom(t)
		_, err := lr.rooms.AddPlayer(ctx, addParams("ABCDE", "Caio"))
		require.NoError(t, err)

		started, err := lr.rooms.StartMatch(ctx, room.StartMatchParams{
			RoomID: lr.roomID, MatchID: matchID(t, lr), Now: lobbyNow.Add(time.Hour),
		})

		require.NoError(t, err)
		assert.NotEmpty(t, started.Round.ID)
		assert.Equal(t, 1, started.Round.Number)
		assert.Equal(t, game.PhaseThemeSelection, started.Round.Phase)
		assert.Len(t, started.Players, 3, "players are re-read inside the transaction")

		match := latestMatch(t, lr.db, lr.roomID)
		assert.Equal(t, "in_progress", match.Status)
		require.NotNil(t, match.StartedAt)
		assert.True(t, match.StartedAt.Equal(lobbyNow.Add(time.Hour)))

		_, err = lr.rooms.AddPlayer(ctx, addParams("ABCDE", "Duda"))
		require.ErrorIs(t, err, room.ErrMatchInProgress, "nobody joins during the theme selection")
	})

	t.Run("refuses a match that already started", func(t *testing.T) {
		lr := newLiveRoom(t)
		params := room.StartMatchParams{RoomID: lr.roomID, MatchID: matchID(t, lr), Now: lobbyNow}
		_, err := lr.rooms.StartMatch(ctx, params)
		require.NoError(t, err)

		_, err = lr.rooms.StartMatch(ctx, params)

		require.Error(t, err)
		assert.Equal(t, 1, count(t, lr.db, (*storage.Round)(nil)))
	})
}

// roundFixture is room ABCDE with Ana, Bia and Caio, a started match, a words theme with two
// words and four questions (three of its own, one generic), and a players theme.
type roundFixture struct {
	liveRoom
	caio    string
	matchID string
	roundID string
	foods   room.Theme
	people  room.Theme
}

func newRoundFixture(t *testing.T) roundFixture {
	t.Helper()
	ctx := context.Background()
	lr := newLiveRoom(t)
	caio, err := lr.rooms.AddPlayer(ctx, addParams("ABCDE", "Caio"))
	require.NoError(t, err)

	foodsRow := &storage.Theme{Slug: "foods", Name: "Comidas", Kind: "words"}
	insert(t, lr.db, foodsRow, &storage.Theme{Slug: "people", Name: "Pessoas da sala", Kind: "players"})
	insert(t, lr.db,
		&[]storage.Word{{ThemeID: foodsRow.ID, Text: "Pizza"}, {ThemeID: foodsRow.ID, Text: "Sushi"}},
		&[]storage.Question{
			{Kind: "words", ThemeID: &foodsRow.ID, Text: "Doce ou salgado?"},
			{Kind: "words", ThemeID: &foodsRow.ID, Text: "Quente ou frio?"},
			{Kind: "words", ThemeID: &foodsRow.ID, Text: "Caro?"},
			{Kind: "words", Text: "Levaria numa viagem?"},
			{Kind: "players", Text: "Essa pessoa chega atrasada?"},
		})

	mID := matchID(t, lr)
	started, err := lr.rooms.StartMatch(ctx, room.StartMatchParams{RoomID: lr.roomID, MatchID: mID, Now: lobbyNow})
	require.NoError(t, err)

	state, err := lr.rooms.LoadRoom(ctx, "ABCDE", lobbyNow)
	require.NoError(t, err)
	var foods, people room.Theme
	for _, th := range state.Themes {
		switch th.Kind {
		case game.ThemeKindWords:
			foods = th
		case game.ThemeKindPlayers:
			people = th
		}
	}
	return roundFixture{liveRoom: lr, caio: caio.ID, matchID: mID, roundID: started.Round.ID, foods: foods, people: people}
}

func (f roundFixture) begin(t *testing.T, wordID int64, questions []room.Question) []string {
	t.Helper()
	ids, err := f.rooms.BeginRound(context.Background(), room.BeginRoundParams{
		RoundID: f.roundID, ThemeID: f.foods.ID, ThemeDrawn: true, WordID: wordID, ImpostorID: f.caio,
		Turns: []room.PlannedTurn{
			{AskerID: f.ana, AnswererID: f.bia, QuestionID: questions[0].ID},
			{AskerID: f.bia, AnswererID: f.caio, QuestionID: questions[1].ID},
			{AskerID: f.caio, AnswererID: f.ana, QuestionID: questions[2].ID},
		},
	})
	require.NoError(t, err)
	return ids
}

func TestRooms_RoundCandidates(t *testing.T) {
	ctx := context.Background()
	f := newRoundFixture(t)

	words, err := f.rooms.LoadRoundCandidates(ctx, f.roomID, f.foods)
	require.NoError(t, err)
	require.Len(t, words.Words, 2)
	assert.Len(t, words.Questions, 4, "the theme's own questions plus the generic ones of its kind")

	people, err := f.rooms.LoadRoundCandidates(ctx, f.roomID, f.people)
	require.NoError(t, err)
	assert.Empty(t, people.Words)
	require.Len(t, people.Questions, 1)
	assert.Equal(t, "Essa pessoa chega atrasada?", people.Questions[0].Text)

	f.begin(t, words.Words[0].ID, words.Questions)

	after, err := f.rooms.LoadRoundCandidates(ctx, f.roomID, f.foods)
	require.NoError(t, err)
	require.Len(t, after.Words, 1, "a word used in the room is not offered again")
	assert.NotEqual(t, words.Words[0].ID, after.Words[0].ID)
}

func TestRooms_RoundFlow(t *testing.T) {
	ctx := context.Background()
	f := newRoundFixture(t)
	candidates, err := f.rooms.LoadRoundCandidates(ctx, f.roomID, f.foods)
	require.NoError(t, err)
	pizza := candidates.Words[0]

	turnIDs := f.begin(t, pizza.ID, candidates.Questions)
	require.Len(t, turnIDs, 3)

	load := func() *room.RoundState {
		t.Helper()
		state, err := f.rooms.LoadRoom(ctx, "ABCDE", lobbyNow)
		require.NoError(t, err)
		require.NotNil(t, state.Round)
		return state.Round
	}

	r := load()
	assert.Equal(t, game.PhaseReveal, r.Phase)
	require.NotNil(t, r.Theme)
	assert.Equal(t, f.foods, *r.Theme)
	assert.True(t, r.ThemeDrawn)
	assert.Equal(t, f.caio, r.ImpostorID)
	assert.Equal(t, pizza.Text, r.Secret)
	require.Len(t, r.Turns, 3)
	assert.Equal(t, room.TurnState{ID: turnIDs[0], Position: 0, AskerID: f.ana, AnswererID: f.bia, Question: candidates.Questions[0].Text}, r.Turns[0])

	t1 := lobbyNow.Add(time.Minute)
	require.NoError(t, f.rooms.StartQuestions(ctx, f.roundID, turnIDs[0], t1, t1.Add(3*time.Minute)))
	r = load()
	assert.Equal(t, game.PhaseQuestions, r.Phase)
	require.NotNil(t, r.Turns[0].DeadlineAt)
	assert.True(t, r.Turns[0].DeadlineAt.Equal(t1.Add(3*time.Minute)))

	require.NoError(t, f.rooms.ExtendTurn(ctx, turnIDs[0], t1.Add(4*time.Minute)))
	assert.True(t, load().Turns[0].DeadlineAt.Equal(t1.Add(4*time.Minute)))

	t2 := t1.Add(2 * time.Minute)
	require.NoError(t, f.rooms.AdvanceTurn(ctx, room.AdvanceTurnParams{
		RoundID: f.roundID, EndedTurnID: turnIDs[0], EndedBy: "answerer", Now: t2,
		NextTurnID: turnIDs[1], NextPosition: 1, NextDeadline: t2.Add(3 * time.Minute),
	}))
	r = load()
	assert.Equal(t, 1, r.CurrentTurn)
	assert.True(t, r.Turns[1].DeadlineAt.Equal(t2.Add(3*time.Minute)))
	require.Error(t, f.rooms.ExtendTurn(ctx, turnIDs[0], t2), "an ended turn cannot be extended")

	require.NoError(t, f.rooms.AdvanceTurn(ctx, room.AdvanceTurnParams{
		RoundID: f.roundID, EndedTurnID: turnIDs[1], EndedBy: "timeout", Now: t2,
		NextTurnID: turnIDs[2], NextPosition: 2, NextDeadline: t2.Add(3 * time.Minute),
	}))
	require.NoError(t, f.rooms.AdvanceTurn(ctx, room.AdvanceTurnParams{
		RoundID: f.roundID, EndedTurnID: turnIDs[2], EndedBy: "host", Now: t2,
		DiscussionDeadline: t2.Add(3 * time.Minute),
	}))
	r = load()
	assert.Equal(t, game.PhaseDiscussion, r.Phase)
	require.NotNil(t, r.PhaseDeadline)
	assert.True(t, r.PhaseDeadline.Equal(t2.Add(3*time.Minute)))

	var endedBy []string
	require.NoError(t, f.db.NewSelect().Model((*storage.Turn)(nil)).Column("ended_by").Order("position").Scan(ctx, &endedBy))
	assert.Equal(t, []string{"answerer", "timeout", "host"}, endedBy)

	require.NoError(t, f.rooms.ExtendDiscussion(ctx, f.roundID, t2.Add(4*time.Minute)))
	assert.True(t, load().PhaseDeadline.Equal(t2.Add(4*time.Minute)))

	votingEnd := t2.Add(10 * time.Minute)
	require.NoError(t, f.rooms.StartVoting(ctx, f.roundID, votingEnd))
	r = load()
	assert.Equal(t, game.PhaseVoting, r.Phase)
	require.NotNil(t, r.PhaseDeadline)
	assert.True(t, r.PhaseDeadline.Equal(votingEnd))
	require.Error(t, f.rooms.StartVoting(ctx, f.roundID, votingEnd), "the voting starts only once")
	require.Error(t, f.rooms.ExtendDiscussion(ctx, f.roundID, t2), "the discussion is over")
}

func TestRooms_BeginRound_PlayersTheme(t *testing.T) {
	ctx := context.Background()
	f := newRoundFixture(t)
	insert(t, f.db, &[]storage.Question{
		{Kind: "players", Text: "Essa pessoa canta?"},
		{Kind: "players", Text: "Essa pessoa dança?"},
	})
	candidates, err := f.rooms.LoadRoundCandidates(ctx, f.roomID, f.people)
	require.NoError(t, err)
	require.Len(t, candidates.Questions, 3)

	_, err = f.rooms.BeginRound(ctx, room.BeginRoundParams{
		RoundID: f.roundID, ThemeID: f.people.ID, SubjectID: f.bia, ImpostorID: f.caio,
		Turns: []room.PlannedTurn{
			{AskerID: f.ana, AnswererID: f.bia, QuestionID: candidates.Questions[0].ID},
			{AskerID: f.bia, AnswererID: f.caio, QuestionID: candidates.Questions[1].ID},
			{AskerID: f.caio, AnswererID: f.ana, QuestionID: candidates.Questions[2].ID},
		},
	})
	require.NoError(t, err)

	state, err := f.rooms.LoadRoom(ctx, "ABCDE", lobbyNow)
	require.NoError(t, err)
	assert.Equal(t, f.bia, state.Round.SubjectID)
	assert.Equal(t, "Bia", state.Round.Secret, "the secret of a players theme is the subject's name")
}

func TestRooms_LoadRoom_PastRoundsOfTheMatch(t *testing.T) {
	ctx := context.Background()
	f := newRoundFixture(t)
	candidates, err := f.rooms.LoadRoundCandidates(ctx, f.roomID, f.foods)
	require.NoError(t, err)
	f.begin(t, candidates.Words[0].ID, candidates.Questions)
	exec(t, f.db.NewUpdate().Model((*storage.Round)(nil)).Set("phase = 'result'").Where("id = ?", f.roundID))
	insert(t, f.db, &storage.Round{MatchID: f.matchID, Number: 2})

	state, err := f.rooms.LoadRoom(ctx, "ABCDE", lobbyNow)

	require.NoError(t, err)
	assert.Equal(t, 2, state.Round.Number)
	assert.Equal(t, game.PhaseThemeSelection, state.Round.Phase)
	assert.Nil(t, state.Round.Theme)
	assert.Empty(t, state.Round.Turns)
	assert.Equal(t, []room.PastRound{{ImpostorID: f.caio}}, state.PastRounds)
}

// votingFixture is a words round (secret Pizza, impostor Caio) moved to the voting.
func newVotingFixture(t *testing.T) (roundFixture, room.Word) {
	t.Helper()
	ctx := context.Background()
	f := newRoundFixture(t)
	candidates, err := f.rooms.LoadRoundCandidates(ctx, f.roomID, f.foods)
	require.NoError(t, err)
	pizza := candidates.Words[0]
	turnIDs := f.begin(t, pizza.ID, candidates.Questions)
	require.NoError(t, f.rooms.StartQuestions(ctx, f.roundID, turnIDs[0], lobbyNow, lobbyNow))
	for i := range 3 {
		params := room.AdvanceTurnParams{RoundID: f.roundID, EndedTurnID: turnIDs[i], EndedBy: "answerer", Now: lobbyNow}
		if i < 2 {
			params.NextTurnID, params.NextPosition, params.NextDeadline = turnIDs[i+1], i+1, lobbyNow
		} else {
			params.DiscussionDeadline = lobbyNow
		}
		require.NoError(t, f.rooms.AdvanceTurn(ctx, params))
	}
	require.NoError(t, f.rooms.StartVoting(ctx, f.roundID, lobbyNow.Add(5*time.Minute)))
	return f, pizza
}

func TestRooms_VotingGuessAndResult(t *testing.T) {
	ctx := context.Background()
	f, pizza := newVotingFixture(t)
	load := func() *room.RoundState {
		t.Helper()
		state, err := f.rooms.LoadRoom(ctx, "ABCDE", lobbyNow)
		require.NoError(t, err)
		return state.Round
	}

	require.NoError(t, f.rooms.CastVote(ctx, f.roundID, f.ana, f.caio, lobbyNow))
	require.NoError(t, f.rooms.CastVote(ctx, f.roundID, f.bia, f.caio, lobbyNow))
	require.Error(t, f.rooms.CastVote(ctx, f.roundID, f.ana, f.bia, lobbyNow), "votes are final")
	require.Error(t, f.rooms.CastVote(ctx, f.roundID, f.caio, f.caio, lobbyNow), "the database refuses self-votes")
	assert.Equal(t, map[string]string{f.ana: f.caio, f.bia: f.caio}, load().Votes)

	words, err := f.rooms.LoadThemeWords(ctx, f.foods.ID)
	require.NoError(t, err)
	require.Len(t, words, 2, "every word of the theme, used or not")

	sushi := words[1]
	options := []string{strconv.FormatInt(sushi.ID, 10), strconv.FormatInt(pizza.ID, 10)}
	guessEnd := lobbyNow.Add(time.Minute)
	require.NoError(t, f.rooms.StartGuess(ctx, room.StartGuessParams{
		RoundID: f.roundID, Abstainers: []string{f.caio}, OptionIDs: options, Deadline: guessEnd,
	}))

	r := load()
	assert.Equal(t, game.PhaseGuess, r.Phase)
	assert.True(t, r.ImpostorFound)
	assert.True(t, r.PhaseDeadline.Equal(guessEnd))
	assert.Equal(t, []room.GuessOption{{ID: options[0], Label: "Sushi"}, {ID: options[1], Label: "Pizza"}}, r.GuessOptions)
	assert.Equal(t, map[string]string{f.ana: f.caio, f.bia: f.caio, f.caio: ""}, r.Votes, "the abstention is recorded")
	assert.Nil(t, r.Guess, "no guess before the result")

	_, err = f.rooms.AddPlayer(ctx, addParams("ABCDE", "Duda"))
	require.ErrorIs(t, err, room.ErrMatchInProgress, "nobody joins during the guess")

	require.NoError(t, f.rooms.FinishRound(ctx, room.FinishRoundParams{
		RoundID: f.roundID, ImpostorFound: true, GuessWordID: pizza.ID, GuessCorrect: true,
		Scores: []game.ScoreEntry{
			{Player: game.PlayerID(f.ana), Reason: game.ScoreCorrectVote, Points: 1},
			{Player: game.PlayerID(f.bia), Reason: game.ScoreCorrectVote, Points: 1},
			{Player: game.PlayerID(f.caio), Reason: game.ScoreImpostorGuessedWord, Points: 1},
		},
	}))

	r = load()
	assert.Equal(t, game.PhaseResult, r.Phase)
	assert.Nil(t, r.PhaseDeadline)
	require.NotNil(t, r.Guess)
	assert.Equal(t, room.GuessState{OptionID: options[1], Correct: true}, *r.Guess)
	assert.Equal(t, map[string]int{f.ana: 1, f.bia: 1, f.caio: 1}, r.Points)

	state, err := f.rooms.LoadRoom(ctx, "ABCDE", lobbyNow)
	require.NoError(t, err)
	for _, p := range state.Players {
		assert.Equal(t, 1, p.Score, "the scoreboard sums round_scores")
	}

	_, err = f.rooms.AddPlayer(ctx, addParams("ABCDE", "Duda"))
	require.NoError(t, err, "players join between rounds")
}

func TestRooms_FinishRound_ImpostorEscaped(t *testing.T) {
	ctx := context.Background()
	f, _ := newVotingFixture(t)
	require.NoError(t, f.rooms.CastVote(ctx, f.roundID, f.ana, f.bia, lobbyNow))

	require.NoError(t, f.rooms.FinishRound(ctx, room.FinishRoundParams{
		RoundID: f.roundID, Abstainers: []string{f.bia, f.caio},
		Scores: []game.ScoreEntry{{Player: game.PlayerID(f.caio), Reason: game.ScoreImpostorEscaped, Points: 2}},
	}))

	state, err := f.rooms.LoadRoom(ctx, "ABCDE", lobbyNow)
	require.NoError(t, err)
	r := state.Round
	assert.Equal(t, game.PhaseResult, r.Phase)
	assert.False(t, r.ImpostorFound)
	assert.Nil(t, r.Guess)
	assert.Equal(t, map[string]string{f.ana: f.bia, f.bia: "", f.caio: ""}, r.Votes)
	assert.Equal(t, map[string]int{f.caio: 2}, r.Points)

	var round storage.Round
	require.NoError(t, f.db.NewSelect().Model(&round).Where("id = ?", f.roundID).Scan(ctx))
	assert.Nil(t, round.GuessCorrect, "no guess when the impostor escaped")
}

func TestRooms_GuessOptionsOfAPlayersTheme(t *testing.T) {
	ctx := context.Background()
	f := newRoundFixture(t)
	insert(t, f.db, &[]storage.Question{{Kind: "players", Text: "Essa pessoa canta?"}, {Kind: "players", Text: "Essa pessoa dança?"}})
	candidates, err := f.rooms.LoadRoundCandidates(ctx, f.roomID, f.people)
	require.NoError(t, err)
	_, err = f.rooms.BeginRound(ctx, room.BeginRoundParams{
		RoundID: f.roundID, ThemeID: f.people.ID, SubjectID: f.bia, ImpostorID: f.caio,
		Turns: []room.PlannedTurn{
			{AskerID: f.ana, AnswererID: f.bia, QuestionID: candidates.Questions[0].ID},
			{AskerID: f.bia, AnswererID: f.caio, QuestionID: candidates.Questions[1].ID},
			{AskerID: f.caio, AnswererID: f.ana, QuestionID: candidates.Questions[2].ID},
		},
	})
	require.NoError(t, err)
	exec(t, f.db.NewUpdate().Model((*storage.Round)(nil)).Set("phase = 'voting'").Where("id = ?", f.roundID))

	require.NoError(t, f.rooms.StartGuess(ctx, room.StartGuessParams{
		RoundID: f.roundID, OptionIDs: []string{f.ana, f.bia}, Deadline: lobbyNow,
	}))

	state, err := f.rooms.LoadRoom(ctx, "ABCDE", lobbyNow)
	require.NoError(t, err)
	assert.Equal(t, []room.GuessOption{{ID: f.ana, Label: "Ana"}, {ID: f.bia, Label: "Bia"}}, state.Round.GuessOptions)

	require.NoError(t, f.rooms.FinishRound(ctx, room.FinishRoundParams{
		RoundID: f.roundID, ImpostorFound: true, GuessPlayerID: f.bia, GuessCorrect: true,
	}))
	state, err = f.rooms.LoadRoom(ctx, "ABCDE", lobbyNow)
	require.NoError(t, err)
	assert.Equal(t, &room.GuessState{OptionID: f.bia, Correct: true}, state.Round.Guess)
}

func TestRooms_NextRoundEndMatchAndPlayAgain(t *testing.T) {
	ctx := context.Background()
	f := newRoundFixture(t)

	id, err := f.rooms.NextRound(ctx, f.matchID, 2, lobbyNow)
	require.NoError(t, err)
	state, err := f.rooms.LoadRoom(ctx, "ABCDE", lobbyNow)
	require.NoError(t, err)
	assert.Equal(t, id, state.Round.ID)
	assert.Equal(t, 2, state.Round.Number)
	assert.Equal(t, game.PhaseThemeSelection, state.Round.Phase)
	_, err = f.rooms.NextRound(ctx, f.matchID, 2, lobbyNow)
	require.Error(t, err, "round numbers are unique in a match")

	require.NoError(t, f.rooms.EndMatch(ctx, f.matchID, game.MatchEndedEarly, lobbyNow))
	match := latestMatch(t, f.db, f.roomID)
	assert.Equal(t, "ended_early", match.Status)
	require.NotNil(t, match.EndedAt)
	require.Error(t, f.rooms.EndMatch(ctx, f.matchID, game.MatchFinished, lobbyNow), "a match ends once")

	_, err = f.rooms.AddPlayer(ctx, addParams("ABCDE", "Duda"))
	require.ErrorIs(t, err, room.ErrMatchFinished)

	next, err := f.rooms.PlayAgain(ctx, room.PlayAgainParams{RoomID: f.roomID, Number: 2, TotalRounds: 8, TurnSeconds: 180, Now: lobbyNow})
	require.NoError(t, err)
	assert.Equal(t, room.MatchState{ID: next.ID, Number: 2, Status: game.MatchLobby, TotalRounds: 8, TurnSeconds: 180}, next)

	state, err = f.rooms.LoadRoom(ctx, "ABCDE", lobbyNow)
	require.NoError(t, err)
	assert.Equal(t, next.ID, state.Match.ID)
	assert.Nil(t, state.Round)
	assert.Empty(t, state.PastRounds)

	_, err = f.rooms.AddPlayer(ctx, addParams("ABCDE", "Duda"))
	require.NoError(t, err, "the new match is in the lobby")
}
