package room

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vhrboliveira/impostor-game/internal/game"
	"github.com/vhrboliveira/impostor-game/internal/protocol"
)

// --- fakes ---------------------------------------------------------------------------------

// fakeLiveStore keeps one room in memory. Tokens map a token hash to a player id.
type fakeLiveStore struct {
	mu sync.Mutex

	state  RoomState
	tokens map[string]string

	loads       int
	emptySince  []*time.Time // every SetEmptySince call, in order
	transfers   []string
	startCalls  int
	startErr    error
	playersInDB []PlayerState // what LoadPlayers and StartMatch return

	// candidates per theme id; LoadRoundCandidates returns them.
	candidates map[int64]RoundCandidates
	began      []BeginRoundParams
	questions  []string // StartQuestions calls: "turnID@deadline"
	extended   []time.Time
	advanced   []AdvanceTurnParams
	discussion []time.Time // ExtendDiscussion deadlines
	votings    int

	themeWords   map[int64][]Word
	castVotes    []string // "voter>target"
	guesses      []StartGuessParams
	finished     []FinishRoundParams
	nextRounds   []int
	endedMatches []game.MatchStatus
	playAgains   []PlayAgainParams
	avatars      map[string]Avatar
	removed      []string

	// failures makes the next calls of a method fail: method name -> how many calls.
	failures map[string]int
	// roomGone simulates the room row deleted by the janitor under a live actor.
	roomGone bool
	// connected lists the MarkConnected calls, in order.
	connected []string
}

var errDatabaseDown = errors.New("database down")

// fail reports whether this call of method must fail. The caller holds f.mu.
func (f *fakeLiveStore) fail(method string) error {
	if f.failures[method] > 0 {
		f.failures[method]--
		return errDatabaseDown
	}
	return nil
}

func wordList(firstID int64, texts ...string) []Word {
	words := make([]Word, len(texts))
	for i, text := range texts {
		words[i] = Word{ID: firstID + int64(i), Text: text}
	}
	return words
}

func questionList(prefix string, n int) []Question {
	out := make([]Question, n)
	for i := range out {
		out[i] = Question{ID: int64(len(prefix)*100 + i), Text: fmt.Sprintf("%s %d?", prefix, i)}
	}
	return out
}

func newFakeLiveStore(now time.Time) *fakeLiveStore {
	players := []PlayerState{
		{ID: "ana", Name: "Ana", IsHost: true, JoinedAt: now.Add(-3 * time.Minute)},
		{ID: "bia", Name: "Bia", JoinedAt: now.Add(-2 * time.Minute)},
		{ID: "caio", Name: "Caio", JoinedAt: now.Add(-1 * time.Minute)},
	}
	created := now.Add(-5 * time.Minute)
	return &fakeLiveStore{
		state: RoomState{
			RoomID:     "room-1",
			Code:       "K7M2P",
			ExpiresAt:  now.Add(24 * time.Hour),
			EmptySince: &created,
			Players:    slices.Clone(players),
			Match:      MatchState{ID: "match-1", Number: 1, Status: game.MatchLobby, TotalRounds: 5, TurnSeconds: 180},
			Themes: []Theme{
				{ID: 1, Name: "Comidas", Kind: game.ThemeKindWords},
				{ID: 2, Name: "Pessoas da sala", Kind: game.ThemeKindPlayers},
				{ID: 3, Name: "Vazio", Kind: game.ThemeKindWords},
			},
		},
		themeWords: map[int64][]Word{1: wordList(11, "Pizza", "Sushi", "Lasanha", "Pastel", "Coxinha", "Feijoada", "Taco", "Ramen", "Bolo", "Pudim")},
		candidates: map[int64]RoundCandidates{
			1: {Words: wordList(11, "Pizza", "Sushi", "Lasanha", "Pastel"), Questions: questionList("comida", 5)},
			2: {Questions: questionList("pessoa", 5)},
			3: {Questions: questionList("vazio", 5)}, // no unused word left
		},
		tokens: map[string]string{
			HashToken("t-ana"):  "ana",
			HashToken("t-bia"):  "bia",
			HashToken("t-caio"): "caio",
		},
		playersInDB: slices.Clone(players),
	}
}

func (f *fakeLiveStore) Authenticate(_ context.Context, code, tokenHash string, _ time.Time) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if code != f.state.Code || f.roomGone {
		return "", "", ErrRoomNotFound
	}
	id, ok := f.tokens[tokenHash]
	if !ok {
		return "", "", ErrUnauthorized
	}
	return id, f.state.RoomID, nil
}

func (f *fakeLiveStore) LoadRoom(_ context.Context, code string, _ time.Time) (RoomState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if code != f.state.Code {
		return RoomState{}, ErrRoomNotFound
	}
	f.loads++
	state := f.state
	state.Players = slices.Clone(f.state.Players)
	return state, nil
}

func (f *fakeLiveStore) LoadPlayers(_ context.Context, roomID, _ string) ([]PlayerState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if roomID != f.state.RoomID {
		return nil, nil // another room: no player
	}
	return slices.Clone(f.playersInDB), nil
}

func (f *fakeLiveStore) SetEmptySince(_ context.Context, _ string, since *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emptySince = append(f.emptySince, since)
	if err := f.fail("SetEmptySince"); err != nil {
		return err
	}
	if f.roomGone {
		return ErrRoomNotFound
	}
	f.state.EmptySince = since
	return nil
}

func (f *fakeLiveStore) MarkConnected(_ context.Context, playerID string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = append(f.connected, playerID)
	return nil
}

func (f *fakeLiveStore) TransferHost(_ context.Context, _ string, playerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.transfers = append(f.transfers, playerID)
	for i := range f.state.Players {
		f.state.Players[i].IsHost = f.state.Players[i].ID == playerID
	}
	for i := range f.playersInDB {
		f.playersInDB[i].IsHost = f.playersInDB[i].ID == playerID
	}
	return nil
}

func (f *fakeLiveStore) LoadRoundCandidates(_ context.Context, _ string, theme Theme) (RoundCandidates, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.candidates[theme.ID], nil
}

func (f *fakeLiveStore) BeginRound(_ context.Context, params BeginRoundParams) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.began = append(f.began, params)
	ids := make([]string, len(params.Turns))
	for i := range ids {
		ids[i] = fmt.Sprintf("turn-%d", i)
	}
	return ids, nil
}

func (f *fakeLiveStore) StartQuestions(_ context.Context, _, turnID string, _, deadline time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.questions = append(f.questions, turnID+"@"+deadline.Format(time.TimeOnly))
	return nil
}

func (f *fakeLiveStore) ExtendTurn(_ context.Context, _ string, deadline time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.extended = append(f.extended, deadline)
	return nil
}

func (f *fakeLiveStore) AdvanceTurn(_ context.Context, params AdvanceTurnParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("AdvanceTurn"); err != nil {
		return err
	}
	f.advanced = append(f.advanced, params)
	return nil
}

func (f *fakeLiveStore) ExtendDiscussion(_ context.Context, _ string, deadline time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.discussion = append(f.discussion, deadline)
	return nil
}

func (f *fakeLiveStore) StartVoting(context.Context, string, time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("StartVoting"); err != nil {
		return err
	}
	f.votings++
	return nil
}

func (f *fakeLiveStore) CastVote(_ context.Context, _, voterID, targetID string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.castVotes = append(f.castVotes, voterID+">"+targetID)
	return nil
}

// LoadGuessWords returns the theme words that no round other than roundID used, like the storage
// query (the rounds of the room are the ones the fake began).
func (f *fakeLiveStore) LoadGuessWords(_ context.Context, _ string, themeID int64, roundID string) ([]Word, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	used := map[int64]bool{}
	for _, b := range f.began {
		if b.RoundID != roundID && b.WordID != 0 {
			used[b.WordID] = true
		}
	}
	var words []Word
	for _, w := range f.themeWords[themeID] {
		if !used[w.ID] {
			words = append(words, w)
		}
	}
	return words, nil
}

func (f *fakeLiveStore) StartGuess(_ context.Context, params StartGuessParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("StartGuess"); err != nil {
		return err
	}
	f.guesses = append(f.guesses, params)
	return nil
}

// FinishRound also adds the points to the players, like the round_scores rows do.
func (f *fakeLiveStore) FinishRound(_ context.Context, params FinishRoundParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("FinishRound"); err != nil {
		return err
	}
	f.finished = append(f.finished, params)
	for _, s := range params.Scores {
		for i := range f.playersInDB {
			if f.playersInDB[i].ID == string(s.Player) {
				f.playersInDB[i].Score += s.Points
			}
		}
	}
	return nil
}

func (f *fakeLiveStore) NextRound(_ context.Context, _ string, number int, _ time.Time) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextRounds = append(f.nextRounds, number)
	return fmt.Sprintf("round-%d", number), nil
}

func (f *fakeLiveStore) EndMatch(_ context.Context, _ string, status game.MatchStatus, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.endedMatches = append(f.endedMatches, status)
	return nil
}

// PlayAgain opens the new match; the scores of the new match start at zero.
func (f *fakeLiveStore) PlayAgain(_ context.Context, params PlayAgainParams) (MatchState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.playAgains = append(f.playAgains, params)
	for i := range f.playersInDB {
		f.playersInDB[i].Score = 0
	}
	return MatchState{
		ID: fmt.Sprintf("match-%d", params.Number), Number: params.Number, Status: game.MatchLobby,
		TotalRounds: params.TotalRounds, TurnSeconds: params.TurnSeconds,
	}, nil
}

func (f *fakeLiveStore) RemovePlayer(_ context.Context, _, playerID, newHostID string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, playerID)
	for _, list := range [][]PlayerState{f.state.Players, f.playersInDB} {
		for i := range list {
			switch list[i].ID {
			case playerID:
				list[i].LeftAt, list[i].IsHost = &at, false
			case newHostID:
				list[i].IsHost = true
			}
		}
	}
	return nil
}

func (f *fakeLiveStore) SetAvatar(_ context.Context, playerID string, image []byte, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.avatars == nil {
		f.avatars = make(map[string]Avatar)
	}
	f.avatars[playerID] = Avatar{Image: slices.Clone(image), UpdatedAt: at}
	return nil
}

func (f *fakeLiveStore) DeleteAvatar(_ context.Context, playerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.avatars, playerID)
	return nil
}

func (f *fakeLiveStore) LoadAvatar(_ context.Context, code, playerID string, _ time.Time) (Avatar, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	avatar, ok := f.avatars[playerID]
	if code != f.state.Code || !ok {
		return Avatar{}, ErrAvatarNotFound
	}
	return avatar, nil
}

func (f *fakeLiveStore) StartMatch(context.Context, StartMatchParams) (StartedMatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startCalls++
	if f.startErr != nil {
		return StartedMatch{}, f.startErr
	}
	return StartedMatch{
		Round:   RoundState{ID: "round-1", Number: 1, Phase: game.PhaseThemeSelection},
		Players: slices.Clone(f.playersInDB),
	}, nil
}

// fakeClock fires timers only when the test advances it.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	at      time.Time
	f       func()
	stopped bool
	fired   bool
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		wasPending := !t.stopped && !t.fired
		t.stopped = true
		return wasPending
	}
}

// Advance moves the clock and runs every due timer, in the test goroutine.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due []*fakeTimer
	for _, t := range c.timers {
		if !t.stopped && !t.fired && !t.at.After(c.now) {
			t.fired = true
			due = append(due, t)
		}
	}
	c.mu.Unlock()

	for _, t := range due {
		t.f()
	}
}

// fakeClient records what the actor sends.
type fakeClient struct {
	mu        sync.Mutex
	messages  []protocol.ServerMessage
	closed    bool
	closeCode int
}

func (c *fakeClient) Send(msg protocol.ServerMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, msg)
}

func (c *fakeClient) Close(code int, _ string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.closeCode = code
}

func (c *fakeClient) lastSnapshot(t *testing.T) protocol.RoomSnapshot {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.messages) - 1; i >= 0; i-- {
		if c.messages[i].Type == protocol.TypeRoomSnapshot {
			return c.messages[i].Payload.(protocol.RoomSnapshot)
		}
	}
	t.Fatal("no snapshot received")
	return protocol.RoomSnapshot{}
}

func (c *fakeClient) lastError() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.messages) - 1; i >= 0; i-- {
		if c.messages[i].Type == protocol.TypeError {
			return c.messages[i].Payload.(protocol.ErrorPayload).Code
		}
	}
	return ""
}

// --- helpers -------------------------------------------------------------------------------

type harness struct {
	t     *testing.T
	store *fakeLiveStore
	clock *fakeClock
	m     *Manager
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	store := newFakeLiveStore(now)
	clock := &fakeClock{now: now}
	m := NewManager(store, slog.New(slog.NewTextHandler(io.Discard, nil)), ManagerConfig{
		HostGracePeriod:    30 * time.Second,
		IdleTimeout:        2 * time.Minute,
		DiscussionDuration: 3 * time.Minute,
	})
	m.clock = clock
	m.newRand = func() game.Rand { return rand.New(rand.NewPCG(1, 2)) }
	return &harness{t: t, store: store, clock: clock, m: m}
}

func (h *harness) connect(token string) (*Conn, *fakeClient) {
	h.t.Helper()
	client := &fakeClient{}
	conn, err := h.m.Connect(context.Background(), "k7m2p", token, client)
	require.NoError(h.t, err)
	return conn, client
}

// settle waits until the actor processed every command queued before it.
func (h *harness) settle() {
	h.m.mu.Lock()
	a := h.m.actors["K7M2P"]
	h.m.mu.Unlock()
	if a != nil {
		a.do(func() {})
		a.do(func() {})
	}
}

func (h *harness) activeActors() int {
	h.m.mu.Lock()
	defer h.m.mu.Unlock()
	return len(h.m.actors)
}

func playerByID(t *testing.T, snapshot protocol.RoomSnapshot, id string) protocol.PlayerView {
	t.Helper()
	for _, p := range snapshot.Players {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("player %s not in snapshot", id)
	return protocol.PlayerView{}
}

// --- tests ---------------------------------------------------------------------------------

func TestConnect_SendsTheLobbySnapshot(t *testing.T) {
	h := newHarness(t)

	conn, client := h.connect("t-ana")

	assert.Equal(t, "ana", conn.PlayerID())
	snapshot := client.lastSnapshot(t)
	assert.Equal(t, "K7M2P", snapshot.RoomCode)
	assert.Equal(t, protocol.MatchView{Status: "lobby", TotalRounds: 5, RoundNumber: 0}, snapshot.Match)
	assert.Nil(t, snapshot.Round)
	assert.Equal(t, protocol.MeView{PlayerID: "ana"}, snapshot.Me)
	require.Len(t, snapshot.Players, 3)
	assert.Equal(t, protocol.PlayerView{ID: "ana", Name: "Ana", IsHost: true, IsConnected: true}, snapshot.Players[0])
	assert.False(t, playerByID(t, snapshot, "bia").IsConnected)
}

func TestConnect_Errors(t *testing.T) {
	cases := []struct {
		name    string
		code    string
		token   string
		wantErr error
	}{
		// No existence oracle: without a valid token for the room, every case is ErrUnauthorized.
		{name: "malformed code", code: "K7", token: "t-ana", wantErr: ErrUnauthorized},
		{name: "unknown room", code: "ZZZZZ", token: "t-ana", wantErr: ErrUnauthorized},
		{name: "missing token", code: "K7M2P", token: "", wantErr: ErrUnauthorized},
		{name: "wrong token", code: "K7M2P", token: "nope", wantErr: ErrUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)

			_, err := h.m.Connect(context.Background(), tc.code, tc.token, &fakeClient{})

			require.ErrorIs(t, err, tc.wantErr)
			assert.Zero(t, h.activeActors())
		})
	}
}

func TestPresence_EveryoneSeesConnectionsAndDisconnections(t *testing.T) {
	h := newHarness(t)
	_, ana := h.connect("t-ana")
	bia, biaClient := h.connect("t-bia")

	assert.True(t, playerByID(t, ana.lastSnapshot(t), "bia").IsConnected)
	assert.True(t, playerByID(t, biaClient.lastSnapshot(t), "ana").IsConnected)
	assert.Equal(t, "bia", biaClient.lastSnapshot(t).Me.PlayerID)

	bia.Leave()
	h.settle()

	assert.False(t, playerByID(t, ana.lastSnapshot(t), "bia").IsConnected)
}

func TestPresence_ASecondTabKeepsThePlayerConnected(t *testing.T) {
	h := newHarness(t)
	_, ana := h.connect("t-ana")
	firstTab, _ := h.connect("t-bia")
	h.connect("t-bia")

	firstTab.Leave()
	h.settle()

	assert.True(t, playerByID(t, ana.lastSnapshot(t), "bia").IsConnected)
}

func TestPresence_PlayerWhoJoinedAfterTheRoomLoadedIsReloaded(t *testing.T) {
	h := newHarness(t)
	_, ana := h.connect("t-ana")

	h.store.mu.Lock()
	h.store.playersInDB = append(h.store.playersInDB, PlayerState{ID: "duda", Name: "Duda", JoinedAt: h.clock.now})
	h.store.tokens[HashToken("t-duda")] = "duda"
	h.store.mu.Unlock()
	h.connect("t-duda")

	duda := playerByID(t, ana.lastSnapshot(t), "duda")
	assert.True(t, duda.IsConnected)
	assert.Equal(t, 1, h.store.loads, "the room is loaded once; only the players are re-read")
}

func TestPresence_TracksEmptySince(t *testing.T) {
	h := newHarness(t)

	ana, _ := h.connect("t-ana")
	bia, _ := h.connect("t-bia")
	require.Len(t, h.store.emptySince, 1)
	assert.Nil(t, h.store.emptySince[0], "the first connection clears empty_since")

	ana.Leave()
	h.settle()
	assert.Len(t, h.store.emptySince, 1, "the room is not empty while Bia is connected")

	h.clock.Advance(10 * time.Second)
	bia.Leave()
	h.settle()

	require.Len(t, h.store.emptySince, 2)
	require.NotNil(t, h.store.emptySince[1])
	assert.Equal(t, h.clock.Now(), *h.store.emptySince[1])
}

func TestHost_TransfersToTheOldestConnectedPlayerAfterTheGracePeriod(t *testing.T) {
	h := newHarness(t)
	ana, _ := h.connect("t-ana")
	_, caio := h.connect("t-caio")
	_, bia := h.connect("t-bia")

	ana.Leave()
	h.settle()
	h.clock.Advance(29 * time.Second)
	h.settle()
	assert.Empty(t, h.store.transfers, "still inside the grace period")
	assert.True(t, playerByID(t, bia.lastSnapshot(t), "ana").IsHost)

	h.clock.Advance(time.Second)
	h.settle()

	assert.Equal(t, []string{"bia"}, h.store.transfers, "Bia joined before Caio")
	snapshot := caio.lastSnapshot(t)
	assert.True(t, playerByID(t, snapshot, "bia").IsHost)
	assert.False(t, playerByID(t, snapshot, "ana").IsHost)
}

func TestHost_KeepsTheRoleWhenBackWithinTheGracePeriod(t *testing.T) {
	h := newHarness(t)
	ana, _ := h.connect("t-ana")
	_, bia := h.connect("t-bia")

	ana.Leave()
	h.settle()
	h.clock.Advance(20 * time.Second)
	h.connect("t-ana")
	h.clock.Advance(time.Minute)
	h.settle()

	assert.Empty(t, h.store.transfers)
	assert.True(t, playerByID(t, bia.lastSnapshot(t), "ana").IsHost)
}

func TestHost_OfflineWhenTheRoomLoadsStartsTheGracePeriod(t *testing.T) {
	h := newHarness(t)
	_, bia := h.connect("t-bia")

	h.clock.Advance(30 * time.Second)
	h.settle()

	assert.Equal(t, []string{"bia"}, h.store.transfers)
	assert.True(t, playerByID(t, bia.lastSnapshot(t), "bia").IsHost)
}

func TestStartMatch(t *testing.T) {
	t.Run("only the host can start", func(t *testing.T) {
		h := newHarness(t)
		h.connect("t-ana")
		h.connect("t-caio")
		bia, biaClient := h.connect("t-bia")

		bia.Handle(protocol.ClientMessage{Type: protocol.TypeStartMatch})
		h.settle()

		assert.Equal(t, protocol.ErrCodeNotHost, biaClient.lastError())
		assert.Zero(t, h.store.startCalls)
	})

	t.Run("needs three connected players", func(t *testing.T) {
		h := newHarness(t)
		ana, anaClient := h.connect("t-ana")
		h.connect("t-bia")

		ana.Handle(protocol.ClientMessage{Type: protocol.TypeStartMatch})
		h.settle()

		assert.Equal(t, protocol.ErrCodeNotEnoughPlayers, anaClient.lastError())
		assert.Zero(t, h.store.startCalls)
	})

	t.Run("moves everyone to the theme selection of round 1", func(t *testing.T) {
		h := newHarness(t)
		ana, _ := h.connect("t-ana")
		_, bia := h.connect("t-bia")
		h.connect("t-caio")

		ana.Handle(protocol.ClientMessage{Type: protocol.TypeStartMatch})
		h.settle()

		snapshot := bia.lastSnapshot(t)
		assert.Equal(t, protocol.MatchView{Status: "in_progress", TotalRounds: 5, RoundNumber: 1}, snapshot.Match)
		require.NotNil(t, snapshot.Round)
		assert.Equal(t, 1, snapshot.Round.Number)
		assert.Equal(t, "theme_selection", snapshot.Round.Phase)
		assert.Equal(t, []protocol.ThemeOption{{ID: 1, Name: "Comidas"}, {ID: 2, Name: "Pessoas da sala"}, {ID: 3, Name: "Vazio"}}, snapshot.Round.ThemeOptions)
		assert.Equal(t, []protocol.TurnView{}, snapshot.Round.Turns)
		assert.Nil(t, snapshot.Round.Result)
	})

	t.Run("cannot start twice", func(t *testing.T) {
		h := newHarness(t)
		ana, anaClient := h.connect("t-ana")
		h.connect("t-bia")
		h.connect("t-caio")

		ana.Handle(protocol.ClientMessage{Type: protocol.TypeStartMatch})
		ana.Handle(protocol.ClientMessage{Type: protocol.TypeStartMatch})
		h.settle()

		assert.Equal(t, protocol.ErrCodeInvalidPhase, anaClient.lastError())
		assert.Equal(t, 1, h.store.startCalls)
	})

	t.Run("a database failure is reported and changes nothing", func(t *testing.T) {
		h := newHarness(t)
		h.store.startErr = errors.New("connection reset")
		ana, anaClient := h.connect("t-ana")
		h.connect("t-bia")
		h.connect("t-caio")

		ana.Handle(protocol.ClientMessage{Type: protocol.TypeStartMatch})
		h.settle()

		assert.Equal(t, protocol.ErrCodeInternal, anaClient.lastError())
		assert.Equal(t, "lobby", anaClient.lastSnapshot(t).Match.Status)
	})
}

func TestActor_StopsWhenIdleAndReloadsOnTheNextConnection(t *testing.T) {
	h := newHarness(t)
	ana, _ := h.connect("t-ana")
	ana.Leave()
	h.settle()

	h.clock.Advance(2 * time.Minute)
	h.settle()
	assert.Zero(t, h.activeActors())

	_, client := h.connect("t-ana")

	assert.Equal(t, 2, h.store.loads)
	assert.True(t, playerByID(t, client.lastSnapshot(t), "ana").IsConnected)
}

func TestActor_ClosesEveryConnectionWhenTheRoomExpires(t *testing.T) {
	h := newHarness(t)
	_, ana := h.connect("t-ana")
	_, bia := h.connect("t-bia")

	h.clock.Advance(24 * time.Hour)
	h.settle()

	for _, c := range []*fakeClient{ana, bia} {
		assert.True(t, c.closed)
		assert.Equal(t, protocol.CloseRoomNotFound, c.closeCode)
	}
	assert.Zero(t, h.activeActors())
}

// A room reloaded with an ended match must not resume the timers of its abandoned round.
func TestLoadRoom_EndedMatchResumesNoTimer(t *testing.T) {
	h := newHarness(t)
	overdue := h.clock.Now().Add(-time.Minute)
	h.store.state.Match.Status = game.MatchEndedEarly
	h.store.state.Round = &RoundState{
		ID: "round-1", Number: 1, Phase: game.PhaseQuestions, Theme: &Theme{ID: 1, Kind: game.ThemeKindWords},
		ImpostorID: "caio", WordID: 11, Secret: "Pizza",
		Turns: []TurnState{
			{ID: "turn-0", Position: 0, AskerID: "ana", AnswererID: "bia", Question: "q0", DeadlineAt: &overdue},
			{ID: "turn-1", Position: 1, AskerID: "bia", AnswererID: "caio", Question: "q1"},
			{ID: "turn-2", Position: 2, AskerID: "caio", AnswererID: "ana", Question: "q2"},
		},
	}

	_, ana := h.connect("t-ana")
	h.clock.Advance(0)
	h.settle()

	assert.Empty(t, h.store.advanced)
	assert.Equal(t, "questions", ana.lastSnapshot(t).Round.Phase)
}

// A voting round loaded from the database has no vote rows yet, so storage leaves Votes nil. The
// first vote and the voting deadline must still work (they used to panic on the nil map and
// crash the whole process).
func TestLoadRoom_VotingWithoutVotes(t *testing.T) {
	load := func(t *testing.T, deadline time.Time) *harness {
		h := newHarness(t)
		h.store.state.Match.Status = game.MatchInProgress
		h.store.state.Round = &RoundState{
			ID: "round-1", Number: 1, Phase: game.PhaseVoting, Theme: &Theme{ID: 1, Kind: game.ThemeKindWords},
			ImpostorID: "caio", WordID: 11, Secret: "Pizza", PhaseDeadline: &deadline,
			Turns: []TurnState{
				{ID: "turn-0", Position: 0, AskerID: "ana", AnswererID: "bia"},
				{ID: "turn-1", Position: 1, AskerID: "bia", AnswererID: "caio"},
				{ID: "turn-2", Position: 2, AskerID: "caio", AnswererID: "ana"},
			},
		}
		return h
	}

	t.Run("first vote", func(t *testing.T) {
		h := load(t, h0().Add(4*time.Minute))
		conn, ana := h.connect("t-ana")
		h.connect("t-bia")
		conn.Handle(protocol.ClientMessage{Type: protocol.TypeVote, Vote: &protocol.VotePayload{TargetID: "caio"}})
		h.settle()

		assert.Equal(t, []string{"ana>caio"}, h.store.castVotes)
		assert.Equal(t, []string{"ana"}, ana.lastSnapshot(t).Round.VotedIDs)
	})

	t.Run("deadline already passed", func(t *testing.T) {
		h := load(t, h0().Add(-time.Second))
		_, bia := h.connect("t-bia")
		h.clock.Advance(0)
		h.settle()

		require.Len(t, h.store.finished, 1)
		assert.ElementsMatch(t, []string{"ana", "bia", "caio"}, h.store.finished[0].Abstainers)
		assert.Equal(t, "result", bia.lastSnapshot(t).Round.Phase)
	})
}

// h0 is the fake clock's start time used by newHarness.
func h0() time.Time { return time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC) }

// A panic inside one room's actor stops only that room: its connections are closed with 1011
// (the web app reconnects), and the Manager keeps serving rooms, this one included.
func TestActor_PanicStopsOnlyThatRoom(t *testing.T) {
	h := newHarness(t)
	_, ana := h.connect("t-ana")
	h.m.mu.Lock()
	a := h.m.actors["K7M2P"]
	h.m.mu.Unlock()

	a.do(func() { panic("boom") })
	select {
	case <-a.done:
	case <-time.After(2 * time.Second):
		t.Fatal("the actor did not stop after the panic")
	}

	ana.mu.Lock()
	closed, code := ana.closed, ana.closeCode
	ana.mu.Unlock()
	assert.True(t, closed)
	assert.Equal(t, protocol.CloseInternalError, code)
	assert.Equal(t, 0, h.activeActors())

	_, bia := h.connect("t-bia")
	assert.Equal(t, "lobby", bia.lastSnapshot(t).Match.Status)
}

// --- empty_since: the marker the janitor relies on must follow the real occupancy -----------

func (h *harness) emptySince() *time.Time {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	return h.store.state.EmptySince
}

func TestEmptySince_RetriesAFailedClear(t *testing.T) {
	h := newHarness(t)
	h.store.failures = map[string]int{"SetEmptySince": 1}

	h.connect("t-ana") // the clear fails: the room still looks empty to the janitor
	require.NotNil(t, h.emptySince())

	h.connect("t-bia")
	assert.Nil(t, h.emptySince(), "the next connection retries the clear")
}

func TestEmptySince_IdleStopMarksTheRoomEmpty(t *testing.T) {
	// After a crash the stored marker still says "occupied". An actor started only by a REST call
	// must not stop leaving it like that, or the room outlives the empty-room deletion.
	h := newHarness(t)
	h.store.state.EmptySince = nil
	require.NoError(t, h.m.DeleteAvatar(context.Background(), "k7m2p", "t-ana"))

	h.clock.Advance(2 * time.Minute)
	h.settle()

	assert.Equal(t, 0, h.activeActors())
	assert.NotNil(t, h.emptySince())
}

func TestEmptySince_FailedMarkIsRetriedBeforeTheActorStops(t *testing.T) {
	h := newHarness(t)
	conn, _ := h.connect("t-ana")
	h.store.mu.Lock()
	h.store.failures = map[string]int{"SetEmptySince": 1}
	h.store.mu.Unlock()

	conn.Leave()
	h.settle()
	require.Nil(t, h.emptySince(), "the mark failed")

	h.clock.Advance(2 * time.Minute)
	h.settle()
	assert.NotNil(t, h.emptySince())
	assert.Equal(t, 0, h.activeActors())
}

func TestEmptySince_RoomDeletedUnderTheActor(t *testing.T) {
	h := newHarness(t)
	conn, _ := h.connect("t-ana")
	h.store.mu.Lock()
	h.store.roomGone = true
	h.store.mu.Unlock()

	conn.Leave()
	h.settle()
	assert.Equal(t, 0, h.activeActors(), "the actor of a deleted room stops at once")
}

// A room code is reused only after its room was deleted. An actor still alive for the old room
// must not serve the new one.
func TestConnect_StaleActorOfAReusedCode(t *testing.T) {
	h := newHarness(t)
	_, ana := h.connect("t-ana")

	h.store.mu.Lock()
	duda := PlayerState{ID: "duda", Name: "Duda", IsHost: true, JoinedAt: h0()}
	h.store.state.RoomID = "room-2"
	h.store.state.Players = []PlayerState{duda}
	h.store.playersInDB = []PlayerState{duda}
	h.store.tokens = map[string]string{HashToken("t-duda"): "duda"}
	h.store.mu.Unlock()

	_, client := h.connect("t-duda")
	players := client.lastSnapshot(t).Players
	require.Len(t, players, 1)
	assert.Equal(t, "duda", players[0].ID)

	ana.mu.Lock()
	defer ana.mu.Unlock()
	assert.True(t, ana.closed)
	assert.Equal(t, protocol.CloseRoomNotFound, ana.closeCode)
}

// A player holds at most maxConnsPerPlayer connections: a new one closes the oldest (a stale
// socket of a reconnecting phone), so connection counts stay bounded.
func TestConnect_CapsConnectionsPerPlayer(t *testing.T) {
	h := newHarness(t)
	var clients []*fakeClient
	for range maxConnsPerPlayer + 1 {
		_, c := h.connect("t-ana")
		clients = append(clients, c)
	}
	h.settle()

	clients[0].mu.Lock()
	closed, code := clients[0].closed, clients[0].closeCode
	clients[0].mu.Unlock()
	assert.True(t, closed, "the oldest connection is closed")
	assert.Equal(t, protocol.ClosePolicyViolation, code)
	for _, c := range clients[1:] {
		c.mu.Lock()
		assert.False(t, c.closed)
		c.mu.Unlock()
	}

	h.m.mu.Lock()
	a := h.m.actors["K7M2P"]
	h.m.mu.Unlock()
	reply := make(chan int, 1)
	a.do(func() { reply <- len(a.conns["ana"]) })
	assert.Equal(t, maxConnsPerPlayer, <-reply)
}

// The first connection of each player is recorded: only players who opened the room (or joined
// a moment ago) hold a seat against the room's limit.
func TestConnect_RecordsTheFirstConnection(t *testing.T) {
	h := newHarness(t)
	h.connect("t-ana")
	h.connect("t-ana")
	h.connect("t-bia")
	h.settle()

	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	assert.Equal(t, []string{"ana", "bia"}, h.store.connected)
}
