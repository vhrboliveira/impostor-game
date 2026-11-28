package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"

	"github.com/vhrboliveira/impostor-game/internal/room"
)

// maxBodyBytes bounds every JSON request body.
const maxBodyBytes = 4 << 10

// RoomService is the room lifecycle the REST endpoints need. *room.Service implements it.
type RoomService interface {
	CreateRoom(ctx context.Context, in room.CreateRoomInput) (room.Session, error)
	JoinRoom(ctx context.Context, code string, in room.JoinRoomInput) (room.Session, error)
}

type createRoomRequest struct {
	PlayerName  string `json:"playerName"`
	TotalRounds int    `json:"totalRounds"`
}

type joinRoomRequest struct {
	PlayerName string `json:"playerName"`
}

// sessionResponse is returned when a player enters a room. The token must be kept on the device:
// it authenticates the WebSocket connection and the server only stores its hash.
type sessionResponse struct {
	RoomCode string `json:"roomCode"`
	PlayerID string `json:"playerId"`
	Token    string `json:"token"`
}

// errRequestTooLarge, errInvalidRequest and errUnsupportedMediaType classify body decoding
// failures.
var (
	errRequestTooLarge      = errors.New("request body too large")
	errInvalidRequest       = errors.New("invalid request body")
	errUnsupportedMediaType = errors.New("request body is not application/json")
)

// handleCreateRoom serves POST /api/room: it creates a room and makes the caller its host.
func handleCreateRoom(logger *slog.Logger, rooms RoomService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createRoomRequest
		if err := decodeJSON(w, r, &req); err != nil {
			writeDecodeError(w, err)
			return
		}

		session, err := rooms.CreateRoom(r.Context(), room.CreateRoomInput{
			PlayerName:  req.PlayerName,
			TotalRounds: req.TotalRounds,
		})
		if err != nil {
			writeRoomError(w, r, logger, err)
			return
		}
		writeJSON(w, http.StatusCreated, newSessionResponse(session))
	}
}

// handleJoinRoom serves POST /api/room/{code}/join: it adds the caller to an existing room.
func handleJoinRoom(logger *slog.Logger, rooms RoomService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req joinRoomRequest
		if err := decodeJSON(w, r, &req); err != nil {
			writeDecodeError(w, err)
			return
		}

		session, err := rooms.JoinRoom(r.Context(), r.PathValue("code"), room.JoinRoomInput{
			PlayerName: req.PlayerName,
		})
		if err != nil {
			writeRoomError(w, r, logger, err)
			return
		}
		writeJSON(w, http.StatusCreated, newSessionResponse(session))
	}
}

func newSessionResponse(s room.Session) sessionResponse {
	return sessionResponse{RoomCode: s.RoomCode, PlayerID: s.PlayerID, Token: s.Token}
}

// decodeJSON reads exactly one JSON value of at most maxBodyBytes into dst, rejecting unknown
// fields. The body must be declared application/json: another site's page cannot send that
// without a CORS preflight, so it cannot post to these endpoints blindly. It returns
// errUnsupportedMediaType, errRequestTooLarge or errInvalidRequest.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		return errUnsupportedMediaType
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		return classifyDecodeError(err)
	}
	// Anything after the first value (a second object, stray text) makes the body invalid.
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errInvalidRequest
		}
		return classifyDecodeError(err)
	}
	return nil
}

func classifyDecodeError(err error) error {
	if maxErr := (*http.MaxBytesError)(nil); errors.As(err, &maxErr) {
		return errRequestTooLarge
	}
	return errInvalidRequest
}

func writeDecodeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errRequestTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "request_too_large")
	case errors.Is(err, errUnsupportedMediaType):
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
	default:
		writeError(w, http.StatusBadRequest, "invalid_request")
	}
}

// writeRoomError maps a room error (from the Service or the Manager) to its HTTP status and stable error code. Anything
// unexpected is logged and reported as internal_error.
func writeRoomError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, room.ErrInvalidName):
		writeError(w, http.StatusBadRequest, "invalid_name")
	case errors.Is(err, room.ErrInvalidRounds):
		writeError(w, http.StatusBadRequest, "invalid_rounds")
	case errors.Is(err, room.ErrRoomNotFound):
		writeError(w, http.StatusNotFound, "room_not_found")
	case errors.Is(err, room.ErrRoomFull):
		writeError(w, http.StatusConflict, "room_full")
	case errors.Is(err, room.ErrMatchInProgress):
		writeError(w, http.StatusConflict, "match_in_progress")
	case errors.Is(err, room.ErrMatchFinished):
		writeError(w, http.StatusConflict, "match_finished")
	case errors.Is(err, room.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, room.ErrInvalidAvatar):
		writeError(w, http.StatusBadRequest, "invalid_avatar")
	case errors.Is(err, room.ErrAvatarLocked):
		writeError(w, http.StatusConflict, "avatar_locked")
	case errors.Is(err, room.ErrAvatarNotFound):
		writeError(w, http.StatusNotFound, "avatar_not_found")
	default:
		logger.ErrorContext(r.Context(), "room request failed",
			slog.Any("error", err),
			slog.String("request_id", RequestIDFromContext(r.Context())),
		)
		writeError(w, http.StatusInternalServerError, "internal_error")
	}
}
