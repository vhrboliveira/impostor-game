package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vhrboliveira/impostor-game/internal/room"
)

// fakeRooms records the last call and returns the configured session or error.
type fakeRooms struct {
	session room.Session
	err     error

	createIn room.CreateRoomInput
	joinCode string
	joinIn   room.JoinRoomInput
	calls    int
}

func (f *fakeRooms) CreateRoom(_ context.Context, in room.CreateRoomInput) (room.Session, error) {
	f.calls++
	f.createIn = in
	return f.session, f.err
}

func (f *fakeRooms) JoinRoom(_ context.Context, code string, in room.JoinRoomInput) (room.Session, error) {
	f.calls++
	f.joinCode = code
	f.joinIn = in
	return f.session, f.err
}

var testSession = room.Session{RoomCode: "ABCDE", PlayerID: "p-1", Token: "secret-token"}

func postJSON(t *testing.T, handler http.Handler, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

func newRoomsRouter(rooms RoomService) http.Handler {
	return NewRouter(Deps{Logger: discardLogger(), DB: fakePinger{}, Rooms: rooms})
}

func TestCreateRoom_Success(t *testing.T) {
	rooms := &fakeRooms{session: testSession}

	rec := postJSON(t, newRoomsRouter(rooms), "/api/room", `{"playerName":"Ana","totalRounds":5}`)

	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, map[string]any{"roomCode": "ABCDE", "playerId": "p-1", "token": "secret-token"}, decodeBody(t, rec))
	assert.Equal(t, room.CreateRoomInput{PlayerName: "Ana", TotalRounds: 5}, rooms.createIn)
}

func TestJoinRoom_Success(t *testing.T) {
	rooms := &fakeRooms{session: testSession}

	rec := postJSON(t, newRoomsRouter(rooms), "/api/room/abcde/join", `{"playerName":"Bia"}`)

	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, map[string]any{"roomCode": "ABCDE", "playerId": "p-1", "token": "secret-token"}, decodeBody(t, rec))
	assert.Equal(t, "abcde", rooms.joinCode, "the service normalizes the code")
	assert.Equal(t, room.JoinRoomInput{PlayerName: "Bia"}, rooms.joinIn)
}

func TestRoomEndpoints_RejectInvalidBodies(t *testing.T) {
	tooLarge := `{"playerName":"` + strings.Repeat("a", maxBodyBytes) + `"}`

	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{name: "malformed JSON", body: `{"playerName":`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "empty body", body: ``, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "unknown field", body: `{"playerName":"Ana","isHost":true}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "wrong type", body: `{"playerName":42}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "two JSON values", body: `{"playerName":"Ana"}{"playerName":"Bia"}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "trailing garbage", body: `{"playerName":"Ana"} x`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "body over 4 KiB", body: tooLarge, wantStatus: http.StatusRequestEntityTooLarge, wantCode: "request_too_large"},
	}

	for _, target := range []string{"/api/room", "/api/room/ABCDE/join"} {
		for _, tc := range cases {
			t.Run(target+" "+tc.name, func(t *testing.T) {
				rooms := &fakeRooms{session: testSession}

				rec := postJSON(t, newRoomsRouter(rooms), target, tc.body)

				assert.Equal(t, tc.wantStatus, rec.Code)
				assert.Equal(t, map[string]any{"code": tc.wantCode}, decodeBody(t, rec))
				assert.Zero(t, rooms.calls, "an invalid body never reaches the service")
			})
		}
	}
}

func TestRoomEndpoints_MapServiceErrors(t *testing.T) {
	cases := []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{err: room.ErrInvalidName, wantStatus: http.StatusBadRequest, wantCode: "invalid_name"},
		{err: room.ErrInvalidRounds, wantStatus: http.StatusBadRequest, wantCode: "invalid_rounds"},
		{err: room.ErrRoomNotFound, wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{err: room.ErrRoomFull, wantStatus: http.StatusConflict, wantCode: "room_full"},
		{err: room.ErrMatchInProgress, wantStatus: http.StatusConflict, wantCode: "match_in_progress"},
		{err: room.ErrMatchFinished, wantStatus: http.StatusConflict, wantCode: "match_finished"},
		{err: fmt.Errorf("add player: %w", room.ErrRoomFull), wantStatus: http.StatusConflict, wantCode: "room_full"},
		{err: room.ErrCodeAllocation, wantStatus: http.StatusInternalServerError, wantCode: "internal_error"},
		{err: errors.New("connection reset"), wantStatus: http.StatusInternalServerError, wantCode: "internal_error"},
	}

	requests := []struct{ target, body string }{
		{target: "/api/room", body: `{"playerName":"Ana","totalRounds":5}`},
		{target: "/api/room/ABCDE/join", body: `{"playerName":"Ana"}`},
	}

	for _, req := range requests {
		for _, tc := range cases {
			t.Run(req.target+" "+tc.err.Error(), func(t *testing.T) {
				rec := postJSON(t, newRoomsRouter(&fakeRooms{err: tc.err}), req.target, req.body)

				assert.Equal(t, tc.wantStatus, rec.Code)
				assert.Equal(t, map[string]any{"code": tc.wantCode}, decodeBody(t, rec))
			})
		}
	}
}

func TestRoomEndpoints_OnlyAcceptPost(t *testing.T) {
	rooms := &fakeRooms{session: testSession}

	// The catch-all route answers any other method with the JSON not_found error.
	rec := do(t, newRoomsRouter(rooms), http.MethodGet, "/api/room")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, map[string]any{"code": "not_found"}, decodeBody(t, rec))
	assert.Zero(t, rooms.calls)
}
