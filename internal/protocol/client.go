package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Message types sent by clients.
const (
	TypePing             = "ping"
	TypeStartMatch       = "start_match"
	TypeSelectTheme      = "select_theme"
	TypeConfirmReveal    = "confirm_reveal"
	TypeStartQuestions   = "start_questions"
	TypeNextTurn         = "next_turn"
	TypeExtendTurn       = "extend_turn"
	TypeExtendDiscussion = "extend_discussion"
	TypeStartVoting      = "start_voting"
	TypeVote             = "vote"
	TypeGuess            = "guess"
	TypeNextRound        = "next_round"
	TypeEndMatch         = "end_match"
	TypePlayAgain        = "play_again"
	// TypeLeaveRoom: the player leaves the room for good (after a confirmation in the app).
	TypeLeaveRoom = "leave_room"
	// TypeRemovePlayer: the host removes another player from the room.
	TypeRemovePlayer = "remove_player"
)

// MaxMessageBytes bounds a message from a client.
const MaxMessageBytes = 4 << 10

// maxIDLength bounds identifiers sent by clients (player ids are UUIDs).
const maxIDLength = 64

// ErrInvalidMessage is returned for any message that does not follow the protocol.
var ErrInvalidMessage = errors.New("invalid message")

// ClientMessage is a validated message from a client. Only the payload field matching Type is
// set.
type ClientMessage struct {
	Type        string
	SelectTheme *SelectThemePayload
	Vote        *VotePayload
	Guess       *GuessPayload
	PlayAgain   *PlayAgainPayload
	Remove      *RemovePlayerPayload
}

// RemovePlayerPayload names the player the host removes.
type RemovePlayerPayload struct {
	PlayerID string
}

// SelectThemePayload is the host's theme choice: a theme id or the random draw ("Sorteio").
type SelectThemePayload struct {
	ThemeID int64
	Random  bool
}

// VotePayload is a vote for a player.
type VotePayload struct {
	TargetID string
}

// GuessPayload is the impostor's pick among the guess options.
type GuessPayload struct {
	OptionID string
}

// PlayAgainPayload starts a new match in the same room with this many rounds.
type PlayAgainPayload struct {
	TotalRounds int
}

type envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// ParseClientMessage decodes and validates one message. Unknown types, unknown fields, missing
// or unexpected payloads all return an error wrapping ErrInvalidMessage.
func ParseClientMessage(data []byte) (ClientMessage, error) {
	if len(data) > MaxMessageBytes {
		return ClientMessage{}, fmt.Errorf("%w: message too large", ErrInvalidMessage)
	}

	var env envelope
	if err := decodeStrict(data, &env); err != nil {
		return ClientMessage{}, err
	}
	hasPayload := len(env.Payload) > 0 && !bytes.Equal(env.Payload, []byte("null"))

	msg := ClientMessage{Type: env.Type}
	switch env.Type {
	case TypePing, TypeStartMatch, TypeConfirmReveal, TypeStartQuestions, TypeNextTurn,
		TypeExtendTurn, TypeExtendDiscussion, TypeStartVoting, TypeNextRound, TypeEndMatch,
		TypeLeaveRoom:
		if hasPayload {
			return ClientMessage{}, fmt.Errorf("%w: %s takes no payload", ErrInvalidMessage, env.Type)
		}

	case TypeSelectTheme:
		if !hasPayload {
			return ClientMessage{}, fmt.Errorf("%w: missing payload", ErrInvalidMessage)
		}
		payload, err := parseSelectTheme(env.Payload)
		if err != nil {
			return ClientMessage{}, err
		}
		msg.SelectTheme = &payload

	case TypeVote:
		if !hasPayload {
			return ClientMessage{}, fmt.Errorf("%w: missing payload", ErrInvalidMessage)
		}
		var raw struct {
			TargetID string `json:"targetId"`
		}
		if err := decodeStrict(env.Payload, &raw); err != nil {
			return ClientMessage{}, err
		}
		if raw.TargetID == "" || len(raw.TargetID) > maxIDLength {
			return ClientMessage{}, fmt.Errorf("%w: invalid targetId", ErrInvalidMessage)
		}
		msg.Vote = &VotePayload{TargetID: raw.TargetID}

	case TypeGuess:
		if !hasPayload {
			return ClientMessage{}, fmt.Errorf("%w: missing payload", ErrInvalidMessage)
		}
		var raw struct {
			OptionID string `json:"optionId"`
		}
		if err := decodeStrict(env.Payload, &raw); err != nil {
			return ClientMessage{}, err
		}
		if raw.OptionID == "" || len(raw.OptionID) > maxIDLength {
			return ClientMessage{}, fmt.Errorf("%w: invalid optionId", ErrInvalidMessage)
		}
		msg.Guess = &GuessPayload{OptionID: raw.OptionID}

	case TypePlayAgain:
		if !hasPayload {
			return ClientMessage{}, fmt.Errorf("%w: missing payload", ErrInvalidMessage)
		}
		var raw struct {
			TotalRounds int `json:"totalRounds"`
		}
		if err := decodeStrict(env.Payload, &raw); err != nil {
			return ClientMessage{}, err
		}
		msg.PlayAgain = &PlayAgainPayload{TotalRounds: raw.TotalRounds}

	case TypeRemovePlayer:
		if !hasPayload {
			return ClientMessage{}, fmt.Errorf("%w: missing payload", ErrInvalidMessage)
		}
		var raw struct {
			PlayerID string `json:"playerId"`
		}
		if err := decodeStrict(env.Payload, &raw); err != nil {
			return ClientMessage{}, err
		}
		if raw.PlayerID == "" || len(raw.PlayerID) > maxIDLength {
			return ClientMessage{}, fmt.Errorf("%w: invalid playerId", ErrInvalidMessage)
		}
		msg.Remove = &RemovePlayerPayload{PlayerID: raw.PlayerID}

	default:
		return ClientMessage{}, fmt.Errorf("%w: unknown type %q", ErrInvalidMessage, env.Type)
	}
	return msg, nil
}

func parseSelectTheme(data json.RawMessage) (SelectThemePayload, error) {
	var raw struct {
		ThemeID json.RawMessage `json:"themeId"`
	}
	if err := decodeStrict(data, &raw); err != nil {
		return SelectThemePayload{}, err
	}

	var random string
	if err := json.Unmarshal(raw.ThemeID, &random); err == nil {
		if random != "random" {
			return SelectThemePayload{}, fmt.Errorf("%w: themeId must be a number or \"random\"", ErrInvalidMessage)
		}
		return SelectThemePayload{Random: true}, nil
	}

	var id int64
	if err := json.Unmarshal(raw.ThemeID, &id); err != nil || id <= 0 {
		return SelectThemePayload{}, fmt.Errorf("%w: themeId must be a number or \"random\"", ErrInvalidMessage)
	}
	return SelectThemePayload{ThemeID: id}, nil
}

// decodeStrict decodes exactly one JSON object into dst, rejecting unknown fields.
func decodeStrict(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidMessage, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing data", ErrInvalidMessage)
	}
	return nil
}
