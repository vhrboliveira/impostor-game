package room

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCode_UsesOnlyTheAlphabetAndHasTheRightLength(t *testing.T) {
	for range 500 {
		code, err := NewCode()

		require.NoError(t, err)
		require.Len(t, code, CodeLength)
		for _, r := range code {
			assert.True(t, strings.ContainsRune(CodeAlphabet, r), "unexpected symbol %q in %q", r, code)
		}
	}
}

func TestCodeAlphabetHasNoAmbiguousCharacters(t *testing.T) {
	assert.Len(t, CodeAlphabet, 32)
	for _, ambiguous := range "IO01" {
		assert.False(t, strings.ContainsRune(CodeAlphabet, ambiguous), "%q must not be in the alphabet", ambiguous)
	}
}

func TestNormalizeCode(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		want   string
		wantOK bool
	}{
		{name: "already normalized", raw: "ABCDE", want: "ABCDE", wantOK: true},
		{name: "lower case and spaces", raw: "  abcde ", want: "ABCDE", wantOK: true},
		{name: "digits are allowed", raw: "a2c3e", want: "A2C3E", wantOK: true},
		{name: "too short", raw: "ABCD", wantOK: false},
		{name: "too long", raw: "ABCDEF", wantOK: false},
		{name: "ambiguous letter", raw: "ABCDO", wantOK: false},
		{name: "ambiguous digit", raw: "ABCD1", wantOK: false},
		{name: "punctuation", raw: "ABC-E", wantOK: false},
		{name: "empty", raw: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := NormalizeCode(tt.raw)

			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewToken_IsRandomAndLongEnough(t *testing.T) {
	first, err := NewToken()
	require.NoError(t, err)
	second, err := NewToken()
	require.NoError(t, err)

	assert.Len(t, first, 43, "32 bytes in unpadded base64")
	assert.NotEqual(t, first, second)
}

func TestHashToken(t *testing.T) {
	assert.Equal(t, HashToken("secret"), HashToken("secret"))
	assert.NotEqual(t, HashToken("secret"), HashToken("Secret"))
	assert.Len(t, HashToken("secret"), 64)
	assert.NotContains(t, HashToken("secret"), "secret")
}

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "plain", raw: "Ana", want: "Ana"},
		{name: "trims spaces", raw: "  Ana  ", want: "Ana"},
		{name: "collapses inner whitespace", raw: "Ana   Maria", want: "Ana Maria"},
		{name: "accents", raw: "João", want: "João"},
		{name: "upper-cases the first letter", raw: "ana", want: "Ana"},
		{name: "upper-cases an accented first letter", raw: "élio", want: "Élio"},
		{name: "keeps the rest as typed", raw: "ana MARIA", want: "Ana MARIA"},
		{name: "first letter after trimming", raw: "  bia", want: "Bia"},
		{name: "no letter to upper-case", raw: "2pac", want: "2pac"},
		{name: "emoji", raw: "😀 Ana", want: "😀 Ana"},
		{name: "exactly the limit", raw: strings.Repeat("a", MaxNameLength), want: "A" + strings.Repeat("a", MaxNameLength-1)},
		{name: "one over the limit", raw: strings.Repeat("a", MaxNameLength+1), wantErr: true},
		{name: "limit counts characters, not bytes", raw: strings.Repeat("ã", MaxNameLength), want: "Ã" + strings.Repeat("ã", MaxNameLength-1)},
		{name: "empty", raw: "", wantErr: true},
		{name: "only spaces", raw: "   ", wantErr: true},
		{name: "control character", raw: "a\x00b", wantErr: true},
		{name: "zero width space", raw: "a\u200bb", wantErr: true},
		{name: "invalid utf-8", raw: "\xff\xfe", wantErr: true},
		// One way to write each name: NFD and NFC spellings collide like any repeated name.
		{name: "decomposed accent becomes composed", raw: "Jose\u0301", want: "José"},
		{name: "two combining marks are fine", raw: "Nguye\u0302\u0303n", want: "Nguyễn"},
		{name: "emoji with variation selector", raw: "\u2764\ufe0f Ana", want: "\u2764\ufe0f Ana"},
		// Invisible or blank characters would make names that look empty or identical.
		{name: "hangul filler", raw: "Ana\u3164", wantErr: true},
		{name: "only a hangul filler", raw: "\u3164", wantErr: true},
		{name: "halfwidth hangul filler", raw: "\uffa0", wantErr: true},
		{name: "braille blank", raw: "Ana\u2800", wantErr: true},
		{name: "combining grapheme joiner", raw: "A\u034fna", wantErr: true},
		{name: "stacked combining marks", raw: "a\u0301\u0302\u0303\u0304", wantErr: true},
		{name: "only combining marks", raw: "\u0301\u0302", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeName(tt.raw)

			if tt.wantErr {
				require.ErrorIs(t, err, ErrInvalidName)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNameCandidate(t *testing.T) {
	assert.Equal(t, "Ana", nameCandidate("Ana", 1))
	assert.Equal(t, "Ana 2", nameCandidate("Ana", 2))
	assert.Equal(t, "Ana 12", nameCandidate("Ana", 12))
}

func TestNameCandidate_TruncatesTheBaseToFitTheLimit(t *testing.T) {
	base := strings.Repeat("a", MaxNameLength)

	got := nameCandidate(base, 12)

	assert.Equal(t, MaxNameLength, len([]rune(got)))
	assert.True(t, strings.HasSuffix(got, " 12"))
}

func TestNameCandidate_DoesNotLeaveASpaceBeforeTheNumber(t *testing.T) {
	// Truncation lands right after the space of "aaaa... b": the space must not be doubled.
	base := strings.Repeat("a", 16) + " bbb"

	got := nameCandidate(base, 12)

	assert.NotContains(t, got, "  ")
	assert.True(t, strings.HasSuffix(got, " 12"))
}
