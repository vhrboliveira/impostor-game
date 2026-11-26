package room

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png" // PNG photos are accepted and converted to JPEG.
	"time"
)

const (
	// MaxAvatarBytes bounds an uploaded photo and the stored one (player_avatars has the same
	// CHECK).
	MaxAvatarBytes = 500 << 10
	// MaxAvatarSide is the largest width or height accepted. The web app crops and scales the
	// photo to a 768x768 square before uploading it (sharp in the reveal on 3x screens).
	MaxAvatarSide = 768

	avatarQuality = 85
)

// Avatar is a player's photo, always a JPEG. UpdatedAt changes with every new photo.
type Avatar struct {
	Image     []byte
	UpdatedAt time.Time
}

// NormalizeAvatar validates an uploaded photo (JPEG or PNG, at most MaxAvatarSide on each side
// and MaxAvatarBytes) and re-encodes it as a JPEG. Re-encoding drops every metadata segment (EXIF
// can carry the GPS position where the photo was taken) and anything hidden after the image data.
// The dimensions are checked before decoding, so a small file cannot claim a huge image.
func NormalizeAvatar(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data) > MaxAvatarBytes {
		return nil, ErrInvalidAvatar
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, ErrInvalidAvatar
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > MaxAvatarSide || cfg.Height > MaxAvatarSide {
		return nil, ErrInvalidAvatar
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrInvalidAvatar
	}

	// Transparent areas of a PNG become white instead of whatever color they hide.
	flat := image.NewRGBA(img.Bounds())
	draw.Draw(flat, flat.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), img, img.Bounds().Min, draw.Over)

	var out bytes.Buffer
	if err := jpeg.Encode(&out, flat, &jpeg.Options{Quality: avatarQuality}); err != nil {
		return nil, fmt.Errorf("encode avatar: %w", err)
	}
	if out.Len() > MaxAvatarBytes {
		return nil, ErrInvalidAvatar
	}
	return out.Bytes(), nil
}

// AvatarURL is the address of a player's photo. The version makes every new photo a new URL, so
// browsers can cache each one for good.
func AvatarURL(code, playerID string, updatedAt time.Time) string {
	return fmt.Sprintf("/api/room/%s/players/%s/avatar?v=%d", code, playerID, updatedAt.UnixMilli())
}

// validPlayerID reports whether id looks like a UUID, so a malformed id from a URL never reaches
// the database (which would fail the uuid cast).
func validPlayerID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, r := range id {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !isHexDigit(r) {
				return false
			}
		}
	}
	return true
}

func isHexDigit(r rune) bool {
	return '0' <= r && r <= '9' || 'a' <= r && r <= 'f' || 'A' <= r && r <= 'F'
}
