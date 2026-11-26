package room

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vhrboliveira/impostor-game/internal/game"
)

var fixedNow = time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

// fakeStore is a hand-written Store that records calls and returns scripted errors.
type fakeStore struct {
	createCalls []CreateRoomParams
	addCalls    []AddPlayerParams

	// createErrs are returned one per call, in order; createErr is returned on every call.
	createErrs []error
	createErr  error
	// addErr decides the result of each AddPlayer call; nil means success.
	addErr func(p AddPlayerParams) error
}

func (f *fakeStore) CreateRoom(_ context.Context, p CreateRoomParams) (Player, error) {
	f.createCalls = append(f.createCalls, p)

	if f.createErr != nil {
		return Player{}, f.createErr
	}
	if len(f.createErrs) > 0 {
		err := f.createErrs[0]
		f.createErrs = f.createErrs[1:]
		if err != nil {
			return Player{}, err
		}
	}
	return Player{ID: "p-host", RoomID: "r1", Name: p.HostName, IsHost: true}, nil
}

func (f *fakeStore) AddPlayer(_ context.Context, p AddPlayerParams) (Player, error) {
	f.addCalls = append(f.addCalls, p)

	if f.addErr != nil {
		if err := f.addErr(p); err != nil {
			return Player{}, err
		}
	}
	return Player{ID: "p-" + p.Name, RoomID: "r1", Name: p.Name}, nil
}

func newTestService(store Store) *Service {
	svc := NewService(store, Config{RoomTTL: 24 * time.Hour})
	svc.now = func() time.Time { return fixedNow }
	svc.newCode = func() (string, error) { return "ABCDE", nil }
	return svc
}

func TestCreateRoom_StoresTheRoomAndReturnsTheSession(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(store)

	session, err := svc.CreateRoom(context.Background(), CreateRoomInput{PlayerName: "  Ana ", TotalRounds: 5})

	require.NoError(t, err)
	assert.Equal(t, "ABCDE", session.RoomCode)
	assert.Equal(t, "p-host", session.PlayerID)
	assert.NotEmpty(t, session.Token)

	require.Len(t, store.createCalls, 1)
	call := store.createCalls[0]
	assert.Equal(t, "ABCDE", call.Code)
	assert.Equal(t, "Ana", call.HostName)
	assert.Equal(t, 5, call.TotalRounds)
	assert.Equal(t, game.DefaultTurnSeconds, call.TurnSeconds)
	assert.Equal(t, fixedNow, call.Now)
	assert.Equal(t, fixedNow.Add(24*time.Hour), call.ExpiresAt)
}

func TestCreateRoom_StoresOnlyTheHashOfTheToken(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(store)

	session, err := svc.CreateRoom(context.Background(), CreateRoomInput{PlayerName: "Ana", TotalRounds: 3})

	require.NoError(t, err)
	require.Len(t, store.createCalls, 1)
	assert.Equal(t, HashToken(session.Token), store.createCalls[0].TokenHash)
	assert.NotEqual(t, session.Token, store.createCalls[0].TokenHash)
}

func TestCreateRoom_RejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		input   CreateRoomInput
		wantErr error
	}{
		{name: "empty name", input: CreateRoomInput{PlayerName: " ", TotalRounds: 5}, wantErr: ErrInvalidName},
		{name: "name too long", input: CreateRoomInput{PlayerName: "abcdefghijklmnopqrstu", TotalRounds: 5}, wantErr: ErrInvalidName},
		{name: "unsupported number of rounds", input: CreateRoomInput{PlayerName: "Ana", TotalRounds: 4}, wantErr: ErrInvalidRounds},
		{name: "zero rounds", input: CreateRoomInput{PlayerName: "Ana", TotalRounds: 0}, wantErr: ErrInvalidRounds},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{}

			_, err := newTestService(store).CreateRoom(context.Background(), tt.input)

			require.ErrorIs(t, err, tt.wantErr)
			assert.Empty(t, store.createCalls, "nothing must be stored")
		})
	}
}

func TestCreateRoom_RetriesWithAnotherCodeWhenTheCodeIsTaken(t *testing.T) {
	store := &fakeStore{createErrs: []error{ErrCodeTaken, ErrCodeTaken}}
	svc := newTestService(store)
	codes := []string{"AAAA2", "BBBB3", "CCCC4"}
	svc.newCode = func() (string, error) {
		code := codes[0]
		codes = codes[1:]
		return code, nil
	}

	session, err := svc.CreateRoom(context.Background(), CreateRoomInput{PlayerName: "Ana", TotalRounds: 5})

	require.NoError(t, err)
	assert.Equal(t, "CCCC4", session.RoomCode)
	assert.Len(t, store.createCalls, 3)
}

func TestCreateRoom_GivesUpAfterTooManyCodeCollisions(t *testing.T) {
	store := &fakeStore{createErr: ErrCodeTaken}

	_, err := newTestService(store).CreateRoom(context.Background(), CreateRoomInput{PlayerName: "Ana", TotalRounds: 5})

	require.ErrorIs(t, err, ErrCodeAllocation)
	assert.Len(t, store.createCalls, maxCodeAttempts)
}

func TestCreateRoom_WrapsUnexpectedStoreErrors(t *testing.T) {
	boom := errors.New("database is down")
	store := &fakeStore{createErr: boom}

	_, err := newTestService(store).CreateRoom(context.Background(), CreateRoomInput{PlayerName: "Ana", TotalRounds: 5})

	require.ErrorIs(t, err, boom)
	assert.Len(t, store.createCalls, 1, "unexpected errors must not be retried")
}

func TestJoinRoom_AddsThePlayerWithANormalizedCode(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(store)

	session, err := svc.JoinRoom(context.Background(), " abcde ", JoinRoomInput{PlayerName: " Bia "})

	require.NoError(t, err)
	assert.Equal(t, "ABCDE", session.RoomCode)
	assert.Equal(t, "p-Bia", session.PlayerID)
	assert.NotEmpty(t, session.Token)

	require.Len(t, store.addCalls, 1)
	call := store.addCalls[0]
	assert.Equal(t, "ABCDE", call.Code)
	assert.Equal(t, "Bia", call.Name)
	assert.Equal(t, HashToken(session.Token), call.TokenHash)
	assert.Equal(t, game.MaxPlayers, call.MaxPlayers)
	assert.Equal(t, fixedNow, call.Now)
}

func TestJoinRoom_RejectsAMalformedCodeWithoutTouchingTheStore(t *testing.T) {
	store := &fakeStore{}

	_, err := newTestService(store).JoinRoom(context.Background(), "abc", JoinRoomInput{PlayerName: "Bia"})

	require.ErrorIs(t, err, ErrRoomNotFound)
	assert.Empty(t, store.addCalls)
}

func TestJoinRoom_RejectsAnInvalidName(t *testing.T) {
	store := &fakeStore{}

	_, err := newTestService(store).JoinRoom(context.Background(), "ABCDE", JoinRoomInput{PlayerName: ""})

	require.ErrorIs(t, err, ErrInvalidName)
	assert.Empty(t, store.addCalls)
}

func TestJoinRoom_AppendsANumberWhenTheNameIsTaken(t *testing.T) {
	taken := map[string]bool{"Ana": true, "Ana 2": true}
	store := &fakeStore{addErr: func(p AddPlayerParams) error {
		if taken[p.Name] {
			return ErrNameTaken
		}
		return nil
	}}

	session, err := newTestService(store).JoinRoom(context.Background(), "ABCDE", JoinRoomInput{PlayerName: "Ana"})

	require.NoError(t, err)
	assert.Equal(t, "p-Ana 3", session.PlayerID)
	require.Len(t, store.addCalls, 3)
	assert.Equal(t, "Ana", store.addCalls[0].Name)
	assert.Equal(t, "Ana 2", store.addCalls[1].Name)
	assert.Equal(t, "Ana 3", store.addCalls[2].Name)
}

func TestJoinRoom_KeepsTheSameTokenAcrossNameRetries(t *testing.T) {
	store := &fakeStore{addErr: func(p AddPlayerParams) error {
		if p.Name == "Ana" {
			return ErrNameTaken
		}
		return nil
	}}

	session, err := newTestService(store).JoinRoom(context.Background(), "ABCDE", JoinRoomInput{PlayerName: "Ana"})

	require.NoError(t, err)
	require.Len(t, store.addCalls, 2)
	assert.Equal(t, store.addCalls[0].TokenHash, store.addCalls[1].TokenHash)
	assert.Equal(t, HashToken(session.Token), store.addCalls[1].TokenHash)
}

func TestJoinRoom_GivesUpAfterTooManyNameCollisions(t *testing.T) {
	store := &fakeStore{addErr: func(AddPlayerParams) error { return ErrNameTaken }}

	_, err := newTestService(store).JoinRoom(context.Background(), "ABCDE", JoinRoomInput{PlayerName: "Ana"})

	require.ErrorIs(t, err, ErrNameAllocation)
	assert.Len(t, store.addCalls, maxNameAttempts)
}

func TestJoinRoom_PropagatesTheStoreSentinels(t *testing.T) {
	for _, sentinel := range []error{ErrRoomNotFound, ErrRoomFull, ErrMatchInProgress} {
		store := &fakeStore{addErr: func(AddPlayerParams) error { return sentinel }}

		_, err := newTestService(store).JoinRoom(context.Background(), "ABCDE", JoinRoomInput{PlayerName: "Bia"})

		require.ErrorIs(t, err, sentinel)
		assert.Len(t, store.addCalls, 1, "%v must not be retried", sentinel)
	}
}

func TestJoinRoom_GivesEveryPlayerADifferentToken(t *testing.T) {
	svc := newTestService(&fakeStore{})

	first, err := svc.JoinRoom(context.Background(), "ABCDE", JoinRoomInput{PlayerName: "Bia"})
	require.NoError(t, err)
	second, err := svc.JoinRoom(context.Background(), "ABCDE", JoinRoomInput{PlayerName: "Caio"})
	require.NoError(t, err)

	assert.NotEqual(t, first.Token, second.Token)
}
