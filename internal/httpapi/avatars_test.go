package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vhrboliveira/impostor-game/internal/room"
)

// fakeAvatars records the last call and returns the configured avatar or error.
type fakeAvatars struct {
	avatar room.Avatar
	err    error

	code, token, playerID string
	image                 []byte
	deleted               bool
}

func (f *fakeAvatars) SetAvatar(_ context.Context, code, token string, image []byte) error {
	f.code, f.token, f.image = code, token, image
	return f.err
}

func (f *fakeAvatars) DeleteAvatar(_ context.Context, code, token string) error {
	f.code, f.token, f.deleted = code, token, true
	return f.err
}

func (f *fakeAvatars) Avatar(_ context.Context, code, playerID string) (room.Avatar, error) {
	f.code, f.playerID = code, playerID
	return f.avatar, f.err
}

func newAvatarRouter(avatars *fakeAvatars) http.Handler {
	return NewRouter(Deps{Logger: discardLogger(), DB: fakePinger{}, Avatars: avatars})
}

func serve(t *testing.T, handler http.Handler, method, target string, body []byte, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestSetAvatar_PassesTheImageAndTheToken(t *testing.T) {
	avatars := &fakeAvatars{}

	rec := serve(t, newAvatarRouter(avatars), http.MethodPut, "/api/room/k7m2p/me/avatar", []byte("jpeg bytes"), "tok")

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "k7m2p", avatars.code)
	assert.Equal(t, "tok", avatars.token)
	assert.Equal(t, []byte("jpeg bytes"), avatars.image)
}

func TestSetAvatar_RejectsABodyOverTheLimit(t *testing.T) {
	avatars := &fakeAvatars{}

	rec := serve(t, newAvatarRouter(avatars), http.MethodPut, "/api/room/K7M2P/me/avatar",
		make([]byte, room.MaxAvatarBytes+1), "tok")

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.Equal(t, "request_too_large", decodeBody(t, rec)["code"])
	assert.Nil(t, avatars.image)
}

func TestAvatarEndpoints_MapErrors(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{err: room.ErrUnauthorized, status: http.StatusUnauthorized, code: "unauthorized"},
		{err: room.ErrRoomNotFound, status: http.StatusNotFound, code: "room_not_found"},
		{err: room.ErrInvalidAvatar, status: http.StatusBadRequest, code: "invalid_avatar"},
		{err: room.ErrAvatarLocked, status: http.StatusConflict, code: "avatar_locked"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			router := newAvatarRouter(&fakeAvatars{err: tc.err})

			for _, method := range []string{http.MethodPut, http.MethodDelete} {
				rec := serve(t, router, method, "/api/room/K7M2P/me/avatar", []byte("x"), "tok")
				assert.Equal(t, tc.status, rec.Code, method)
				assert.Equal(t, tc.code, decodeBody(t, rec)["code"], method)
			}
		})
	}
}

func TestDeleteAvatar(t *testing.T) {
	avatars := &fakeAvatars{}

	rec := serve(t, newAvatarRouter(avatars), http.MethodDelete, "/api/room/K7M2P/me/avatar", nil, "tok")

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.True(t, avatars.deleted)
	assert.Equal(t, "tok", avatars.token)
}

func TestBearerToken(t *testing.T) {
	for header, want := range map[string]string{
		"":              "",
		"Bearer abc":    "abc",
		"Bearer  abc  ": "abc",
		"Basic abc":     "",
		"bearer abc":    "",
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		assert.Equal(t, want, bearerToken(req), header)
	}
}

func TestGetAvatar_ServesTheJPEGWithCacheHeaders(t *testing.T) {
	at := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	avatars := &fakeAvatars{avatar: room.Avatar{Image: []byte("jpeg"), UpdatedAt: at}}
	router := newAvatarRouter(avatars)

	current := serve(t, router, http.MethodGet, "/api/room/K7M2P/players/p-1/avatar?v=1795608000000", nil, "")
	require.Equal(t, http.StatusOK, current.Code)
	assert.Equal(t, "jpeg", current.Body.String())
	assert.Equal(t, "image/jpeg", current.Header().Get("Content-Type"))
	assert.Equal(t, "nosniff", current.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "private, max-age=31536000, immutable", current.Header().Get("Cache-Control"))
	assert.Equal(t, "K7M2P", avatars.code)
	assert.Equal(t, "p-1", avatars.playerID)

	// Another version (or none) is never cached for good: it may change.
	for _, target := range []string{"/api/room/K7M2P/players/p-1/avatar", "/api/room/K7M2P/players/p-1/avatar?v=1"} {
		rec := serve(t, router, http.MethodGet, target, nil, "")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "private, no-cache", rec.Header().Get("Cache-Control"), target)
	}
}

func TestGetAvatar_NotFound(t *testing.T) {
	rec := serve(t, newAvatarRouter(&fakeAvatars{err: room.ErrAvatarNotFound}), http.MethodGet,
		"/api/room/K7M2P/players/p-1/avatar", nil, "")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "avatar_not_found", decodeBody(t, rec)["code"])
}

func TestAvatarRoutes_OnlyWhenTheServiceIsSet(t *testing.T) {
	router := NewRouter(Deps{Logger: discardLogger(), DB: fakePinger{}})

	rec := serve(t, router, http.MethodPut, "/api/room/K7M2P/me/avatar", []byte("x"), "tok")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "not_found", decodeBody(t, rec)["code"])
}
