//go:build integration

package realtime_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"

	"github.com/vhrboliveira/impostor-game/internal/httpapi"
	"github.com/vhrboliveira/impostor-game/internal/protocol"
	"github.com/vhrboliveira/impostor-game/internal/realtime"
	"github.com/vhrboliveira/impostor-game/internal/room"
	"github.com/vhrboliveira/impostor-game/internal/storage"
	"github.com/vhrboliveira/impostor-game/internal/storage/storagetest"
)

const origin = "http://localhost:5173"

type session struct {
	RoomCode string `json:"roomCode"`
	PlayerID string `json:"playerId"`
	Token    string `json:"token"`
}

type app struct {
	t   *testing.T
	db  *bun.DB
	url string
}

// newApp runs the real router (REST + WebSocket) on a real database.
func newApp(t *testing.T) *app {
	t.Helper()
	db := storagetest.NewMigratedDB(t)
	ctx := context.Background()
	foods := &storage.Theme{Slug: "foods", Name: "Comidas", Kind: "words"}
	_, err := db.NewInsert().Model(foods).Returning("id").Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewInsert().Model(&storage.Word{ThemeID: foods.ID, Text: "Pizza"}).Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewInsert().Model(&[]storage.Question{
		{Kind: "words", ThemeID: &foods.ID, Text: "Doce ou salgado?"},
		{Kind: "words", ThemeID: &foods.ID, Text: "Quente ou frio?"},
		{Kind: "words", Text: "Levaria numa viagem?"},
	}).Exec(ctx)
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	rooms := storage.NewRooms(db)
	shutdown, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	manager := room.NewManager(rooms, logger, room.ManagerConfig{})
	router := httpapi.NewRouter(httpapi.Deps{
		Logger:   logger,
		DB:       db,
		Rooms:    room.NewService(rooms, room.Config{RoomTTL: time.Hour}),
		Avatars:  manager,
		Realtime: realtime.NewHandler(realtime.ManagerConnector{Manager: manager}, logger, []string{origin}, shutdown),
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return &app{t: t, db: db, url: server.URL}
}

func (a *app) post(path, body string) (int, map[string]any) {
	a.t.Helper()
	req, err := http.NewRequestWithContext(a.t.Context(), http.MethodPost, a.url+path, strings.NewReader(body))
	require.NoError(a.t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(a.t, err)
	defer func() { _ = resp.Body.Close() }()

	var out map[string]any
	require.NoError(a.t, json.NewDecoder(resp.Body).Decode(&out))
	return resp.StatusCode, out
}

func (a *app) enter(path, body string) session {
	a.t.Helper()
	status, out := a.post(path, body)
	require.Equal(a.t, http.StatusCreated, status, out)
	return session{RoomCode: out["roomCode"].(string), PlayerID: out["playerId"].(string), Token: out["token"].(string)}
}

func (a *app) dial(s session) *websocket.Conn {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(a.t.Context(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(a.url, "http") + "/ws/room/" + s.RoomCode + "?token=" + s.Token
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {origin}}})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	require.NoError(a.t, err)
	a.t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

// waitSnapshot reads messages until a snapshot satisfies ok.
func waitSnapshot(t *testing.T, conn *websocket.Conn, ok func(protocol.RoomSnapshot) bool) protocol.RoomSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		require.NoError(t, err, "no matching snapshot arrived")

		var msg struct {
			Type    string                `json:"type"`
			Payload protocol.RoomSnapshot `json:"payload"`
		}
		require.NoError(t, json.Unmarshal(data, &msg))
		if msg.Type == protocol.TypeRoomSnapshot && ok(msg.Payload) {
			return msg.Payload
		}
	}
}

// waitError skips snapshots until an error message arrives and returns it as JSON.
func waitError(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		require.NoError(t, err, "no error message arrived")
		var msg struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(data, &msg))
		if msg.Type == protocol.TypeError {
			return string(data)
		}
	}
}

func send(t *testing.T, conn *websocket.Conn, msg string) {
	t.Helper()
	require.NoError(t, conn.Write(t.Context(), websocket.MessageText, []byte(msg)))
}

func inPhase(phase string) func(protocol.RoomSnapshot) bool {
	return func(s protocol.RoomSnapshot) bool { return s.Round != nil && s.Round.Phase == phase }
}

// onlyRoom reads the single room of the test database.
func onlyRoom(t *testing.T, db *bun.DB) storage.Room {
	t.Helper()
	var r storage.Room
	require.NoError(t, db.NewSelect().Model(&r).Scan(context.Background()))
	return r
}

func connectedCount(s protocol.RoomSnapshot) int {
	n := 0
	for _, p := range s.Players {
		if p.IsConnected {
			n++
		}
	}
	return n
}

func TestEndToEnd_CreateJoinConnectAndStart(t *testing.T) {
	a := newApp(t)

	host := a.enter("/api/room", `{"playerName":"ana","totalRounds":3}`)
	bia := a.enter("/api/room/"+strings.ToLower(host.RoomCode)+"/join", `{"playerName":"Bia"}`)
	sameName := a.enter("/api/room/"+host.RoomCode+"/join", `{"playerName":"ANA"}`)

	anaConn := a.dial(host)
	biaConn := a.dial(bia)
	thirdConn := a.dial(sameName)

	lobby := waitSnapshot(t, anaConn, func(s protocol.RoomSnapshot) bool { return connectedCount(s) == 3 })
	assert.Equal(t, host.RoomCode, lobby.RoomCode)
	assert.Equal(t, "lobby", lobby.Match.Status)
	assert.Equal(t, 3, lobby.Match.TotalRounds)
	assert.Equal(t, host.PlayerID, lobby.Me.PlayerID)
	names := []string{lobby.Players[0].Name, lobby.Players[1].Name, lobby.Players[2].Name}
	assert.Equal(t, []string{"Ana", "Bia", "ANA"}, names, "first letter upper-cased; ANA differs from Ana")
	assert.True(t, lobby.Players[0].IsHost)

	assert.Nil(t, onlyRoom(t, a.db).EmptySince, "connecting cleared empty_since")

	require.NoError(t, biaConn.Write(t.Context(), websocket.MessageText, []byte(`{"type":"start_match"}`)))
	assert.JSONEq(t, `{"type":"error","payload":{"code":"not_host"}}`, waitError(t, biaConn))

	require.NoError(t, anaConn.Write(t.Context(), websocket.MessageText, []byte(`{"type":"start_match"}`)))
	for _, conn := range []*websocket.Conn{anaConn, biaConn, thirdConn} {
		started := waitSnapshot(t, conn, func(s protocol.RoomSnapshot) bool { return s.Round != nil })
		assert.Equal(t, "in_progress", started.Match.Status)
		assert.Equal(t, 1, started.Match.RoundNumber)
		assert.Equal(t, "theme_selection", started.Round.Phase)
		require.Len(t, started.Round.ThemeOptions, 1)
		assert.Equal(t, "Comidas", started.Round.ThemeOptions[0].Name)
	}

	status, body := a.post("/api/room/"+host.RoomCode+"/join", `{"playerName":"Duda"}`)
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "match_in_progress", body["code"])

	for _, conn := range []*websocket.Conn{biaConn, thirdConn, anaConn} {
		require.NoError(t, conn.Close(websocket.StatusNormalClosure, ""))
	}
	assert.Eventually(t, func() bool {
		var r storage.Room
		err := a.db.NewSelect().Model(&r).Scan(context.Background())
		return err == nil && r.EmptySince != nil
	}, 5*time.Second, 20*time.Millisecond, "the last disconnection sets empty_since")
}

func TestEndToEnd_FinalCloseCodes(t *testing.T) {
	a := newApp(t)
	host := a.enter("/api/room", `{"playerName":"Ana","totalRounds":3}`)

	readClose := func(s session) websocket.StatusCode {
		conn := a.dial(s)
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return websocket.CloseStatus(err)
			}
		}
	}

	assert.Equal(t, websocket.StatusCode(protocol.CloseUnauthorized),
		readClose(session{RoomCode: host.RoomCode, Token: "not-a-token"}))
	assert.Equal(t, websocket.StatusCode(protocol.CloseRoomNotFound),
		readClose(session{RoomCode: "ZZZZZ", Token: host.Token}))
}

func TestEndToEnd_PlayAMatch(t *testing.T) {
	a := newApp(t)
	host := a.enter("/api/room", `{"playerName":"Ana","totalRounds":3}`)
	sessions := map[string]session{
		host.PlayerID: host,
	}
	for _, name := range []string{"Bia", "Caio"} {
		s := a.enter("/api/room/"+host.RoomCode+"/join", `{"playerName":"`+name+`"}`)
		sessions[s.PlayerID] = s
	}
	conns := map[string]*websocket.Conn{}
	for id, s := range sessions {
		conns[id] = a.dial(s)
	}
	waitSnapshot(t, conns[host.PlayerID], func(s protocol.RoomSnapshot) bool { return connectedCount(s) == 3 })

	send(t, conns[host.PlayerID], `{"type":"start_match"}`)
	selection := waitSnapshot(t, conns[host.PlayerID], inPhase("theme_selection"))
	themeID := selection.Round.ThemeOptions[0].ID
	send(t, conns[host.PlayerID], `{"type":"select_theme","payload":{"themeId":`+strconv.FormatInt(themeID, 10)+`}}`)

	impostors := 0
	var impostor string
	for id, conn := range conns {
		reveal := waitSnapshot(t, conn, inPhase("reveal"))
		assert.Equal(t, "Comidas", *reveal.Round.ThemeName)
		assert.Equal(t, 3, reveal.Round.TotalTurns)
		require.NotNil(t, reveal.Me.Role)
		if *reveal.Me.Role == protocol.RoleImpostor {
			impostors++
			impostor = id
			assert.Nil(t, reveal.Me.Secret)
		} else {
			assert.Equal(t, "Pizza", *reveal.Me.Secret)
		}
	}
	assert.Equal(t, 1, impostors)

	for _, conn := range conns {
		send(t, conn, `{"type":"confirm_reveal"}`)
	}
	questions := waitSnapshot(t, conns[host.PlayerID], inPhase("questions"))
	require.Len(t, questions.Round.Turns, 1)
	require.NotNil(t, questions.Round.Turns[0].DeadlineAt)

	current := questions
	for position := range 3 {
		send(t, conns[current.Round.Turns[position].AnswererID], `{"type":"next_turn"}`)
		if position < 2 {
			current = waitSnapshot(t, conns[host.PlayerID], func(s protocol.RoomSnapshot) bool {
				return s.Round != nil && s.Round.Phase == "questions" && len(s.Round.Turns) == position+2
			})
			assert.Equal(t, current.Round.Turns[position].AnswererID, current.Round.Turns[position+1].AskerID)
		}
	}

	discussion := waitSnapshot(t, conns[host.PlayerID], inPhase("discussion"))
	assert.Len(t, discussion.Round.Turns, 3)
	require.NotNil(t, discussion.Round.PhaseDeadlineAt)

	send(t, conns[host.PlayerID], `{"type":"extend_discussion"}`)
	extended := waitSnapshot(t, conns[host.PlayerID], func(s protocol.RoomSnapshot) bool {
		return s.Round != nil && s.Round.PhaseDeadlineAt != nil && s.Round.PhaseDeadlineAt.After(*discussion.Round.PhaseDeadlineAt)
	})
	assert.Equal(t, time.Minute, extended.Round.PhaseDeadlineAt.Sub(*discussion.Round.PhaseDeadlineAt))

	send(t, conns[host.PlayerID], `{"type":"start_voting"}`)
	for _, conn := range conns {
		voting := waitSnapshot(t, conn, inPhase("voting"))
		assert.Len(t, voting.Round.Turns, 3)
		require.NotNil(t, voting.Round.PhaseDeadlineAt)
	}

	// Both insiders find the impostor, who votes for an insider.
	var insiders []string
	for id := range sessions {
		if id != impostor {
			insiders = append(insiders, id)
		}
	}
	send(t, conns[insiders[0]], `{"type":"vote","payload":{"targetId":"`+impostor+`"}}`)
	send(t, conns[insiders[1]], `{"type":"vote","payload":{"targetId":"`+impostor+`"}}`)
	send(t, conns[impostor], `{"type":"vote","payload":{"targetId":"`+insiders[0]+`"}}`)

	guess := waitSnapshot(t, conns[impostor], inPhase("guess"))
	require.Len(t, guess.Round.GuessOptions, 1, "the theme has a single word")
	assert.Equal(t, "Pizza", guess.Round.GuessOptions[0].Label)
	send(t, conns[impostor], `{"type":"guess","payload":{"optionId":"`+guess.Round.GuessOptions[0].ID+`"}}`)

	for _, conn := range conns {
		result := waitSnapshot(t, conn, inPhase("result")).Round.Result
		require.NotNil(t, result)
		assert.Equal(t, impostor, result.ImpostorID)
		assert.True(t, result.ImpostorFound)
		assert.Equal(t, "Pizza", result.Secret)
		require.NotNil(t, result.Guess)
		assert.True(t, result.Guess.Correct)
		assert.Len(t, result.Votes, 3)
		assert.Len(t, result.Points, 3, "two right votes and the right guess")
	}

	send(t, conns[host.PlayerID], `{"type":"next_round"}`)
	second := waitSnapshot(t, conns[host.PlayerID], func(s protocol.RoomSnapshot) bool {
		return s.Round != nil && s.Round.Number == 2
	})
	assert.Equal(t, "theme_selection", second.Round.Phase)
	for _, p := range second.Players {
		assert.Equal(t, 1, p.Score, "the scores of round 1 carry over")
	}

	send(t, conns[host.PlayerID], `{"type":"end_match"}`)
	podium := waitSnapshot(t, conns[insiders[0]], func(s protocol.RoomSnapshot) bool { return s.Match.Status == "ended_early" })
	assert.Len(t, podium.Players, 3)

	send(t, conns[host.PlayerID], `{"type":"play_again","payload":{"totalRounds":5}}`)
	lobby := waitSnapshot(t, conns[insiders[1]], func(s protocol.RoomSnapshot) bool { return s.Match.Status == "lobby" })
	assert.Equal(t, 5, lobby.Match.TotalRounds)
	assert.Nil(t, lobby.Round)
	for _, p := range lobby.Players {
		assert.Zero(t, p.Score)
	}

	var matches []storage.Match
	require.NoError(t, a.db.NewSelect().Model(&matches).Order("number").Scan(t.Context()))
	require.Len(t, matches, 2)
	assert.Equal(t, "ended_early", matches[0].Status)
	assert.Equal(t, "lobby", matches[1].Status)
}
