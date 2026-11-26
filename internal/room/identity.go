package room

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// CodeAlphabet has 32 symbols and no ambiguous characters (no I, O, 0 or 1). Because 32
	// divides 256, masking a random byte with 31 picks symbols uniformly.
	CodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	// CodeLength is the length of a room code.
	CodeLength = 5
	// MaxNameLength is the maximum length of a player name, in characters.
	MaxNameLength = 20

	tokenBytes = 32
)

// NewCode returns a random room code.
func NewCode() (string, error) {
	var raw [CodeLength]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}

	code := make([]byte, CodeLength)
	for i, b := range raw {
		code[i] = CodeAlphabet[b&31]
	}
	return string(code), nil
}

// NormalizeCode trims and upper-cases a code typed by a player. ok is false when the result
// cannot be a valid room code.
func NormalizeCode(raw string) (code string, ok bool) {
	code = strings.ToUpper(strings.TrimSpace(raw))
	if len(code) != CodeLength {
		return "", false
	}
	for i := 0; i < len(code); i++ {
		if strings.IndexByte(CodeAlphabet, code[i]) < 0 {
			return "", false
		}
	}
	return code, true
}

// NewToken returns a random session token (32 bytes, URL-safe base64).
func NewToken() (string, error) {
	var raw [tokenBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// HashToken returns the hex SHA-256 of a session token. Only the hash is stored.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NormalizeName validates a player name: it collapses whitespace, requires between 1 and
// MaxNameLength printable characters, and upper-cases the first letter, so "ana" and "Ana" are
// the same player name. The rest of the name is kept as typed.
func NormalizeName(raw string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", ErrInvalidName
	}

	name := strings.Join(strings.Fields(raw), " ")
	if name == "" || utf8.RuneCountInString(name) > MaxNameLength {
		return "", ErrInvalidName
	}
	for _, r := range name {
		if !unicode.IsPrint(r) {
			return "", ErrInvalidName
		}
	}

	first, size := utf8.DecodeRuneInString(name)
	return string(unicode.ToTitle(first)) + name[size:], nil
}

// nameCandidate returns the name to try on the given attempt: the name itself first, then
// "name 2", "name 3"... truncating the base so the result still fits MaxNameLength.
func nameCandidate(base string, attempt int) string {
	if attempt <= 1 {
		return base
	}

	suffix := " " + strconv.Itoa(attempt)
	runes := []rune(base)
	if limit := MaxNameLength - utf8.RuneCountInString(suffix); len(runes) > limit {
		runes = runes[:limit]
	}
	return strings.TrimRight(string(runes), " ") + suffix
}
