package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/vhrboliveira/impostor-game/internal/room"
)

// AvatarService is what the photo endpoints need. *room.Manager implements it.
type AvatarService interface {
	SetAvatar(ctx context.Context, code, token string, image []byte) error
	DeleteAvatar(ctx context.Context, code, token string) error
	Avatar(ctx context.Context, code, playerID string) (room.Avatar, error)
}

// handleSetAvatar serves PUT /api/room/{code}/me/avatar: the body is the image itself (JPEG or
// PNG, at most room.MaxAvatarBytes) and the player is identified by their session token in
// "Authorization: Bearer <token>". The new photo reaches everyone in the next snapshot.
func handleSetAvatar(logger *slog.Logger, avatars AvatarService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		image, err := io.ReadAll(http.MaxBytesReader(w, r.Body, room.MaxAvatarBytes))
		if err != nil {
			writeDecodeError(w, classifyDecodeError(err))
			return
		}
		if err := avatars.SetAvatar(r.Context(), r.PathValue("code"), bearerToken(r), image); err != nil {
			writeRoomError(w, r, logger, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleDeleteAvatar serves DELETE /api/room/{code}/me/avatar.
func handleDeleteAvatar(logger *slog.Logger, avatars AvatarService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := avatars.DeleteAvatar(r.Context(), r.PathValue("code"), bearerToken(r)); err != nil {
			writeRoomError(w, r, logger, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleGetAvatar serves GET /api/room/{code}/players/{id}/avatar. It needs no token: image
// elements cannot send one, and the URL holds the player's random id, which only the players of
// the room receive. The URL of the snapshot carries the photo's version (?v=), so that exact URL
// is cached for good; any other is revalidated.
func handleGetAvatar(logger *slog.Logger, avatars AvatarService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		avatar, err := avatars.Avatar(r.Context(), r.PathValue("code"), r.PathValue("id"))
		if err != nil {
			writeRoomError(w, r, logger, err)
			return
		}

		h := w.Header()
		h.Set("Content-Type", "image/jpeg")
		h.Set("Content-Length", strconv.Itoa(len(avatar.Image)))
		h.Set("X-Content-Type-Options", "nosniff")
		if r.URL.Query().Get("v") == strconv.FormatInt(avatar.UpdatedAt.UnixMilli(), 10) {
			h.Set("Cache-Control", "private, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "private, no-cache")
		}
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(avatar.Image); err != nil {
			logger.DebugContext(r.Context(), "write avatar", slog.Any("error", err))
		}
	}
}

// bearerToken returns the session token of "Authorization: Bearer <token>", or "".
func bearerToken(r *http.Request) string {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(token)
}
