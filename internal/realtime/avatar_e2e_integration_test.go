//go:build integration

package realtime_test

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vhrboliveira/impostor-game/internal/protocol"
)

// response is a finished HTTP exchange: the body is already read and closed.
type response struct {
	status int
	header http.Header
	body   []byte
}

func (a *app) do(method, path, token string, body []byte) response {
	a.t.Helper()
	req, err := http.NewRequestWithContext(a.t.Context(), method, a.url+path, bytes.NewReader(body))
	require.NoError(a.t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(a.t, err)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(a.t, err)
	return response{status: resp.StatusCode, header: resp.Header, body: data}
}

func avatarOf(s protocol.RoomSnapshot, playerID string) *string {
	for _, p := range s.Players {
		if p.ID == playerID {
			return p.AvatarURL
		}
	}
	return nil
}

func TestEndToEnd_PlayerPhoto(t *testing.T) {
	a := newApp(t)
	host := a.enter("/api/room", `{"playerName":"Ana","totalRounds":3}`)
	bia := a.enter("/api/room/"+host.RoomCode+"/join", `{"playerName":"Bia"}`)
	anaConn := a.dial(host)
	waitSnapshot(t, anaConn, func(s protocol.RoomSnapshot) bool { return len(s.Players) == 2 })

	img := image.NewRGBA(image.Rect(0, 0, 300, 300))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	img.Set(10, 10, color.Black)
	var photo bytes.Buffer
	require.NoError(t, jpeg.Encode(&photo, img, nil))

	// Bia sends her photo right after joining, before opening her WebSocket.
	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodPut, "/api/room/"+host.RoomCode+"/me/avatar", "wrong", photo.Bytes()).status)
	resp := a.do(http.MethodPut, "/api/room/"+host.RoomCode+"/me/avatar", bia.Token, photo.Bytes())
	require.Equal(t, http.StatusNoContent, resp.status)

	withPhoto := waitSnapshot(t, anaConn, func(s protocol.RoomSnapshot) bool { return avatarOf(s, bia.PlayerID) != nil })
	url := *avatarOf(withPhoto, bia.PlayerID)
	assert.Nil(t, avatarOf(withPhoto, host.PlayerID))

	got := a.do(http.MethodGet, url, "", nil)
	require.Equal(t, http.StatusOK, got.status)
	assert.Equal(t, "image/jpeg", got.header.Get("Content-Type"))
	assert.Equal(t, "private, max-age=31536000, immutable", got.header.Get("Cache-Control"))
	cfg, format, err := image.DecodeConfig(bytes.NewReader(got.body))
	require.NoError(t, err)
	assert.Equal(t, "jpeg", format)
	assert.Equal(t, 300, cfg.Width)

	// Removing it in the lobby.
	require.Equal(t, http.StatusNoContent, a.do(http.MethodDelete, "/api/room/"+host.RoomCode+"/me/avatar", bia.Token, nil).status)
	waitSnapshot(t, anaConn, func(s protocol.RoomSnapshot) bool { return avatarOf(s, bia.PlayerID) == nil })
	assert.Equal(t, http.StatusNotFound, a.do(http.MethodGet, url, "", nil).status)
}
