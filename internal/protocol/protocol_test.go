package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseClientMessage_Valid(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want ClientMessage
	}{
		{name: "ping", raw: `{"type":"ping"}`, want: ClientMessage{Type: TypePing}},
		{name: "null payload is no payload", raw: `{"type":"start_match","payload":null}`, want: ClientMessage{Type: TypeStartMatch}},
		{name: "next turn", raw: `{"type":"next_turn"}`, want: ClientMessage{Type: TypeNextTurn}},
		{name: "extend turn", raw: `{"type":"extend_turn"}`, want: ClientMessage{Type: TypeExtendTurn}},
		{name: "end match", raw: `{"type":"end_match"}`, want: ClientMessage{Type: TypeEndMatch}},
		{name: "confirm reveal", raw: `{"type":"confirm_reveal"}`, want: ClientMessage{Type: TypeConfirmReveal}},
		{name: "start questions", raw: `{"type":"start_questions"}`, want: ClientMessage{Type: TypeStartQuestions}},
		{name: "start voting", raw: `{"type":"start_voting"}`, want: ClientMessage{Type: TypeStartVoting}},
		{name: "extend discussion", raw: `{"type":"extend_discussion"}`, want: ClientMessage{Type: TypeExtendDiscussion}},
		{name: "next round", raw: `{"type":"next_round"}`, want: ClientMessage{Type: TypeNextRound}},
		{
			name: "guess",
			raw:  `{"type":"guess","payload":{"optionId":"42"}}`,
			want: ClientMessage{Type: TypeGuess, Guess: &GuessPayload{OptionID: "42"}},
		},
		{
			name: "leave the room",
			raw:  `{"type":"leave_room"}`,
			want: ClientMessage{Type: TypeLeaveRoom},
		},
		{
			name: "remove a player",
			raw:  `{"type":"remove_player","payload":{"playerId":"p2"}}`,
			want: ClientMessage{Type: TypeRemovePlayer, Remove: &RemovePlayerPayload{PlayerID: "p2"}},
		},
		{
			name: "play again",
			raw:  `{"type":"play_again","payload":{"totalRounds":5}}`,
			want: ClientMessage{Type: TypePlayAgain, PlayAgain: &PlayAgainPayload{TotalRounds: 5}},
		},
		{
			name: "select a theme by id",
			raw:  `{"type":"select_theme","payload":{"themeId":7}}`,
			want: ClientMessage{Type: TypeSelectTheme, SelectTheme: &SelectThemePayload{ThemeID: 7}},
		},
		{
			name: "select a random theme",
			raw:  `{"type":"select_theme","payload":{"themeId":"random"}}`,
			want: ClientMessage{Type: TypeSelectTheme, SelectTheme: &SelectThemePayload{Random: true}},
		},
		{
			name: "vote",
			raw:  `{"type":"vote","payload":{"targetId":"0b8a"}}`,
			want: ClientMessage{Type: TypeVote, Vote: &VotePayload{TargetID: "0b8a"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseClientMessage([]byte(tc.raw))

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseClientMessage_Invalid(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{name: "not JSON", raw: `ping`},
		{name: "empty", raw: ``},
		{name: "array", raw: `[]`},
		{name: "unknown type", raw: `{"type":"cheat"}`},
		{name: "missing type", raw: `{}`},
		{name: "server type", raw: `{"type":"room_snapshot"}`},
		{name: "unknown field", raw: `{"type":"ping","extra":1}`},
		{name: "payload where none is expected", raw: `{"type":"start_match","payload":{}}`},
		{name: "two messages", raw: `{"type":"ping"}{"type":"ping"}`},
		{name: "select theme without payload", raw: `{"type":"select_theme"}`},
		{name: "select theme with another string", raw: `{"type":"select_theme","payload":{"themeId":"pizza"}}`},
		{name: "select theme with zero", raw: `{"type":"select_theme","payload":{"themeId":0}}`},
		{name: "select theme with a fraction", raw: `{"type":"select_theme","payload":{"themeId":1.5}}`},
		{name: "select theme without id", raw: `{"type":"select_theme","payload":{}}`},
		{name: "select theme with unknown field", raw: `{"type":"select_theme","payload":{"themeId":1,"x":2}}`},
		{name: "vote without payload", raw: `{"type":"vote"}`},
		{name: "vote with empty target", raw: `{"type":"vote","payload":{"targetId":""}}`},
		{name: "vote with a huge target", raw: `{"type":"vote","payload":{"targetId":"` + strings.Repeat("a", 65) + `"}}`},
		{name: "guess without payload", raw: `{"type":"guess"}`},
		{name: "guess with empty option", raw: `{"type":"guess","payload":{"optionId":""}}`},
		{name: "guess with a number", raw: `{"type":"guess","payload":{"optionId":42}}`},
		{name: "play again without payload", raw: `{"type":"play_again"}`},
		{name: "leave with a payload", raw: `{"type":"leave_room","payload":{}}`},
		{name: "remove without a player", raw: `{"type":"remove_player","payload":{"playerId":""}}`},
		{name: "play again with text", raw: `{"type":"play_again","payload":{"totalRounds":"5"}}`},
		{name: "next round with payload", raw: `{"type":"next_round","payload":{}}`},
		{name: "too large", raw: `{"type":"ping","payload":"` + strings.Repeat("a", MaxMessageBytes) + `"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseClientMessage([]byte(tc.raw))

			require.ErrorIs(t, err, ErrInvalidMessage)
		})
	}
}

func encode(t *testing.T, v any) map[string]any {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}

func TestServerMessages_JSONShape(t *testing.T) {
	t.Run("lobby snapshot uses camelCase and null round", func(t *testing.T) {
		msg := SnapshotMessage(RoomSnapshot{
			RoomCode: "K7M2P",
			Match:    MatchView{Status: "lobby", TotalRounds: 5},
			Players:  []PlayerView{{ID: "p1", Name: "Ana", IsHost: true, IsConnected: true}},
			Me:       MeView{PlayerID: "p1"},
		})

		assert.Equal(t, map[string]any{
			"type": "room_snapshot",
			"payload": map[string]any{
				"roomCode": "K7M2P",
				"match":    map[string]any{"status": "lobby", "totalRounds": float64(5), "roundNumber": float64(0)},
				"players": []any{map[string]any{
					"id": "p1", "name": "Ana", "isHost": true, "isConnected": true, "score": float64(0),
					"avatarUrl": nil, "hasLeft": false,
				}},
				"round": nil,
				"me":    map[string]any{"playerId": "p1", "role": nil, "secret": nil, "voteTargetId": nil},
			},
		}, encode(t, msg))
	})

	t.Run("round lists are arrays, never null", func(t *testing.T) {
		deadline := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
		round := RoundView{
			Number:       1,
			Phase:        "questions",
			ThemeOptions: []ThemeOption{},
			ConfirmedIDs: []string{},
			Turns: []TurnView{{
				Position: 0, AskerID: "a", AnswererID: "b", Question: "Q?", DeadlineAt: &deadline,
			}},
		}

		got := encode(t, round)

		assert.Equal(t, []any{}, got["themeOptions"])
		assert.Equal(t, []any{}, got["confirmedIds"])
		assert.Nil(t, got["result"])
		assert.Nil(t, got["themeName"])
		assert.Nil(t, got["phaseDeadlineAt"])
		assert.InDelta(t, 0, got["totalTurns"], 0)
		assert.Equal(t, "2026-11-25T12:00:00Z", got["turns"].([]any)[0].(map[string]any)["deadlineAt"])
	})

	t.Run("error and pong", func(t *testing.T) {
		assert.Equal(t, map[string]any{"type": "error", "payload": map[string]any{"code": "not_host"}},
			encode(t, ErrorMessage(ErrCodeNotHost)))
		assert.Equal(t, map[string]any{"type": "pong"}, encode(t, PongMessage()))
	})
}
