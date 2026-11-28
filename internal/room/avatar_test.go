package room

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vhrboliveira/impostor-game/internal/game"
)

func photo(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 120, A: 255})
		}
	}
	return img
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, nil))
	return buf.Bytes()
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// withEXIF inserts an APP1 (EXIF) segment right after the JPEG start marker.
func withEXIF(jpg []byte, payload string) []byte {
	segment := append([]byte("Exif\x00\x00"), payload...)
	length := len(segment) + 2
	out := append([]byte{}, jpg[:2]...)
	out = append(out, 0xFF, 0xE1, byte(length>>8), byte(length))
	out = append(out, segment...)
	return append(out, jpg[2:]...)
}

func TestNormalizeAvatar_ReencodesValidPhotosAsJPEG(t *testing.T) {
	transparent := image.NewNRGBA(image.Rect(0, 0, 64, 64)) // every pixel fully transparent

	cases := []struct {
		name string
		data []byte
		w, h int
	}{
		{name: "jpeg at the maximum size", data: encodeJPEG(t, photo(MaxAvatarSide, MaxAvatarSide)), w: MaxAvatarSide, h: MaxAvatarSide},
		{name: "small jpeg", data: encodeJPEG(t, photo(40, 30)), w: 40, h: 30},
		{name: "png", data: encodePNG(t, photo(200, 200)), w: 200, h: 200},
		{name: "transparent png", data: encodePNG(t, transparent), w: 64, h: 64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := NormalizeAvatar(tc.data)
			require.NoError(t, err)

			img, format, err := image.Decode(bytes.NewReader(out))
			require.NoError(t, err)
			assert.Equal(t, "jpeg", format)
			assert.Equal(t, image.Rect(0, 0, tc.w, tc.h), img.Bounds())
			assert.LessOrEqual(t, len(out), MaxAvatarBytes)
		})
	}

	t.Run("transparent areas become white", func(t *testing.T) {
		out, err := NormalizeAvatar(encodePNG(t, transparent))
		require.NoError(t, err)
		img, err := jpeg.Decode(bytes.NewReader(out))
		require.NoError(t, err)
		r, g, b, _ := img.At(10, 10).RGBA()
		assert.Greater(t, r>>8, uint32(240))
		assert.Greater(t, g>>8, uint32(240))
		assert.Greater(t, b>>8, uint32(240))
	})
}

func TestNormalizeAvatar_DropsMetadata(t *testing.T) {
	data := withEXIF(encodeJPEG(t, photo(64, 64)), "GPS -23.5505,-46.6333")
	require.Contains(t, string(data), "GPS -23.5505")

	out, err := NormalizeAvatar(data)

	require.NoError(t, err)
	assert.NotContains(t, string(out), "Exif")
	assert.NotContains(t, string(out), "GPS")
}

func TestNormalizeAvatar_RejectsInvalidImages(t *testing.T) {
	var gifData bytes.Buffer
	require.NoError(t, gif.Encode(&gifData, photo(32, 32), nil))

	cases := []struct {
		name string
		data []byte
	}{
		{name: "empty", data: nil},
		{name: "not an image", data: []byte("hello, this is not a photo")},
		{name: "gif", data: gifData.Bytes()},
		{name: "too wide", data: encodeJPEG(t, photo(MaxAvatarSide+1, 100))},
		{name: "too tall", data: encodeJPEG(t, photo(100, MaxAvatarSide+1))},
		{name: "too many bytes", data: make([]byte, MaxAvatarBytes+1)},
		{name: "truncated jpeg", data: encodeJPEG(t, photo(100, 100))[:300]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeAvatar(tc.data)
			assert.ErrorIs(t, err, ErrInvalidAvatar)
		})
	}
}

func TestAvatarURL_CarriesTheVersion(t *testing.T) {
	at := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	assert.Equal(t,
		"/api/room/K7M2P/players/9f0c2c1e-8f61-4a54-9d0b-5c1f1f6f8a10/avatar?v=1795608000000",
		AvatarURL("K7M2P", "9f0c2c1e-8f61-4a54-9d0b-5c1f1f6f8a10", at))
}

func TestValidPlayerID(t *testing.T) {
	assert.True(t, validPlayerID("9f0c2c1e-8f61-4a54-9d0b-5c1f1f6f8a10"))
	assert.True(t, validPlayerID("9F0C2C1E-8F61-4A54-9D0B-5C1F1F6F8A10"))
	for _, id := range []string{"", "ana", "9f0c2c1e8f614a549d0b5c1f1f6f8a10", "9f0c2c1e-8f61-4a54-9d0b-5c1f1f6f8a1g", "9f0c2c1e-8f61-4a54-9d0b_5c1f1f6f8a10"} {
		assert.False(t, validPlayerID(id), id)
	}
}

// --- the photo through the room actor ------------------------------------------------------

func TestSetAvatar_InTheLobbyEveryoneSeesTheNewURL(t *testing.T) {
	h := newHarness(t)
	_, ana := h.connect("t-ana")
	_, bia := h.connect("t-bia")

	require.NoError(t, h.m.SetAvatar(context.Background(), "k7m2p", "t-ana", encodeJPEG(t, photo(64, 64))))
	h.settle()

	want := AvatarURL("K7M2P", "ana", h.clock.Now())
	for _, client := range []*fakeClient{ana, bia} {
		url := playerByID(t, client.lastSnapshot(t), "ana").AvatarURL
		require.NotNil(t, url)
		assert.Equal(t, want, *url)
	}
	assert.Nil(t, playerByID(t, bia.lastSnapshot(t), "bia").AvatarURL)

	stored, err := h.m.store.LoadAvatar(context.Background(), "K7M2P", "ana", h.clock.Now())
	require.NoError(t, err)
	_, format, err := image.DecodeConfig(bytes.NewReader(stored.Image))
	require.NoError(t, err)
	assert.Equal(t, "jpeg", format)

	// A new photo in the lobby gets a new URL.
	h.clock.Advance(time.Minute)
	require.NoError(t, h.m.SetAvatar(context.Background(), "K7M2P", "t-ana", encodeJPEG(t, photo(64, 64))))
	h.settle()
	assert.Equal(t, AvatarURL("K7M2P", "ana", h.clock.Now()), *playerByID(t, bia.lastSnapshot(t), "ana").AvatarURL)

	// And it can be removed.
	require.NoError(t, h.m.DeleteAvatar(context.Background(), "K7M2P", "t-ana"))
	h.settle()
	assert.Nil(t, playerByID(t, bia.lastSnapshot(t), "ana").AvatarURL)
	_, err = h.m.store.LoadAvatar(context.Background(), "K7M2P", "ana", h.clock.Now())
	assert.ErrorIs(t, err, ErrAvatarNotFound)
}

func TestSetAvatar_DuringAMatchOnlyTheFirstPhotoIsAccepted(t *testing.T) {
	h := newHarness(t)
	h.store.state.Match.Status = game.MatchInProgress
	had := h.clock.Now().Add(-time.Hour)
	h.store.state.Players[1].AvatarAt = &had // bia already has a photo
	_, client := h.connect("t-ana")
	jpg := encodeJPEG(t, photo(64, 64))

	// ana has none yet (she may have joined between rounds): her first photo is accepted.
	require.NoError(t, h.m.SetAvatar(context.Background(), "K7M2P", "t-ana", jpg))
	h.settle()
	assert.NotNil(t, playerByID(t, client.lastSnapshot(t), "ana").AvatarURL)

	// Changing or removing a photo waits for the lobby.
	assert.ErrorIs(t, h.m.SetAvatar(context.Background(), "K7M2P", "t-ana", jpg), ErrAvatarLocked)
	assert.ErrorIs(t, h.m.SetAvatar(context.Background(), "K7M2P", "t-bia", jpg), ErrAvatarLocked)
	assert.ErrorIs(t, h.m.DeleteAvatar(context.Background(), "K7M2P", "t-bia"), ErrAvatarLocked)
	assert.Equal(t, AvatarURL("K7M2P", "bia", had), *playerByID(t, client.lastSnapshot(t), "bia").AvatarURL)
}

func TestSetAvatar_WithoutConnectionsLoadsTheRoom(t *testing.T) {
	h := newHarness(t)
	// duda joined over REST and sends her photo before opening the WebSocket.
	h.store.tokens[HashToken("t-duda")] = "duda"
	h.store.playersInDB = append(h.store.playersInDB, PlayerState{ID: "duda", Name: "Duda", JoinedAt: h.clock.Now()})

	require.NoError(t, h.m.SetAvatar(context.Background(), "K7M2P", "t-duda", encodeJPEG(t, photo(64, 64))))
	assert.Equal(t, 1, h.activeActors())

	_, client := h.connect("t-duda")
	assert.NotNil(t, playerByID(t, client.lastSnapshot(t), "duda").AvatarURL)
}

func TestSetAvatar_Errors(t *testing.T) {
	h := newHarness(t)
	jpg := encodeJPEG(t, photo(64, 64))

	assert.ErrorIs(t, h.m.SetAvatar(context.Background(), "K7M2P", "t-ana", []byte("nope")), ErrInvalidAvatar)
	// A caller without a valid token learns nothing about the room: no existence oracle.
	assert.ErrorIs(t, h.m.SetAvatar(context.Background(), "K7", "t-ana", jpg), ErrUnauthorized)
	assert.ErrorIs(t, h.m.SetAvatar(context.Background(), "ZZZZZ", "t-ana", jpg), ErrUnauthorized)
	assert.ErrorIs(t, h.m.DeleteAvatar(context.Background(), "ZZZZZ", "t-ana"), ErrUnauthorized)
	assert.ErrorIs(t, h.m.SetAvatar(context.Background(), "K7M2P", "", jpg), ErrUnauthorized)
	assert.ErrorIs(t, h.m.SetAvatar(context.Background(), "K7M2P", "t-wrong", jpg), ErrUnauthorized)
	assert.ErrorIs(t, h.m.DeleteAvatar(context.Background(), "K7M2P", "t-wrong"), ErrUnauthorized)
	// The token is checked before the image is decoded: an anonymous upload costs no decoding.
	assert.ErrorIs(t, h.m.SetAvatar(context.Background(), "K7M2P", "t-wrong", []byte("nope")), ErrUnauthorized)
}

// progressiveJPEG builds a valid 768x768 grayscale progressive JPEG made of n identical AC
// refinement scans. Each 12-byte scan walks every block of the image, so decoding cost grows with
// n while the file stays tiny.
func progressiveJPEG(n int) []byte {
	b := []byte{0xFF, 0xD8} // SOI
	b = append(b, 0xFF, 0xDB, 0x00, 0x43, 0x00)
	b = append(b, bytes.Repeat([]byte{0x01}, 64)...)                                            // DQT
	b = append(b, 0xFF, 0xC2, 0x00, 0x0B, 0x08, 0x03, 0x00, 0x03, 0x00, 0x01, 0x01, 0x11, 0x00) // SOF2 768x768
	b = append(b, 0xFF, 0xC4, 0x00, 0x14, 0x10, 0x01)
	b = append(b, make([]byte, 15)...)
	b = append(b, 0xE0) // DHT: one AC code for an end-of-band run
	for range n {
		b = append(b, 0xFF, 0xDA, 0x00, 0x08, 0x01, 0x01, 0x00, 0x01, 0x3F, 0x10, 0x7F, 0xFD)
	}
	return append(b, 0xFF, 0xD9) // EOI
}

// Decoding cost grows with the number of progressive scans, not only with the pixels: a JPEG
// with more scans than any real encoder writes is refused before it is decoded.
func TestNormalizeAvatar_RejectsTooManyJPEGScans(t *testing.T) {
	_, err := NormalizeAvatar(progressiveJPEG(10))
	require.NoError(t, err, "a progressive JPEG with a normal number of scans is fine")

	start := time.Now()
	_, err = NormalizeAvatar(progressiveJPEG(maxJPEGScans + 1))
	assert.ErrorIs(t, err, ErrInvalidAvatar)
	assert.Less(t, time.Since(start), 50*time.Millisecond, "refused without decoding")
}

// Decoding runs under a process-wide limit and gives up when the request goes away.
func TestSetAvatar_DecodesUnderAConcurrencyLimit(t *testing.T) {
	h := newHarness(t)
	for range cap(h.m.avatarSlots) {
		h.m.avatarSlots <- struct{}{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := h.m.SetAvatar(ctx, "K7M2P", "t-ana", encodeJPEG(t, photo(64, 64)))
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestAvatar_ValidatesTheIDsBeforeTheStore(t *testing.T) {
	h := newHarness(t)
	id := "9f0c2c1e-8f61-4a54-9d0b-5c1f1f6f8a10"
	at := h.clock.Now()
	require.NoError(t, h.store.SetAvatar(context.Background(), id, []byte{1, 2, 3}, at))

	avatar, err := h.m.Avatar(context.Background(), "k7m2p", id)
	require.NoError(t, err)
	assert.Equal(t, Avatar{Image: []byte{1, 2, 3}, UpdatedAt: at}, avatar)

	_, err = h.m.Avatar(context.Background(), "K7M2P", "not-a-uuid")
	assert.ErrorIs(t, err, ErrAvatarNotFound)
	_, err = h.m.Avatar(context.Background(), "K7", id)
	assert.ErrorIs(t, err, ErrAvatarNotFound)
}
