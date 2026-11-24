package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var fixedNow = time.Date(2026, 11, 23, 15, 4, 5, 0, time.UTC)

func TestCreateMigration_WritesTransactionalUpAndDownFiles(t *testing.T) {
	dir := t.TempDir()

	paths, err := createMigration(dir, "add_player_avatar", fixedNow)

	require.NoError(t, err)
	assert.Equal(t, []string{
		filepath.Join(dir, "20261123150405_add_player_avatar.tx.up.sql"),
		filepath.Join(dir, "20261123150405_add_player_avatar.tx.down.sql"),
	}, paths)
	for _, path := range paths {
		assert.FileExists(t, path)
	}
}

func TestCreateMigration_UsesUTC(t *testing.T) {
	saoPaulo := time.FixedZone("BRT", -3*60*60)
	local := time.Date(2026, 11, 23, 23, 30, 0, 0, saoPaulo)

	paths, err := createMigration(t.TempDir(), "late_night", local)

	require.NoError(t, err)
	assert.Contains(t, paths[0], "20261124023000_late_night")
}

func TestCreateMigration_RejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"", "Has Caps", "with-dash", "trailing_", "_leading", "double__underscore"} {
		_, err := createMigration(t.TempDir(), name, fixedNow)

		assert.Error(t, err, "name %q should be rejected", name)
	}
}

func TestCreateMigration_NeverOverwritesExistingFiles(t *testing.T) {
	dir := t.TempDir()
	_, err := createMigration(dir, "once", fixedNow)
	require.NoError(t, err)
	existing := filepath.Join(dir, "20261123150405_once.tx.up.sql")
	require.NoError(t, os.WriteFile(existing, []byte("-- keep me"), 0o644))

	_, err = createMigration(dir, "once", fixedNow)

	require.Error(t, err)
	content, readErr := os.ReadFile(existing)
	require.NoError(t, readErr)
	assert.Equal(t, "-- keep me", string(content))
}

func TestRunCreate_ParsesTheDirFlagAndPrintsTheFiles(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer

	err := runCreate([]string{"-dir", dir, "add_something"}, &out, func() time.Time { return fixedNow })

	require.NoError(t, err)
	assert.Contains(t, out.String(), "20261123150405_add_something.tx.up.sql")
	assert.Contains(t, out.String(), "20261123150405_add_something.tx.down.sql")
}

func TestRunCreate_RequiresExactlyOneName(t *testing.T) {
	var out bytes.Buffer

	err := runCreate(nil, &out, time.Now)

	require.Error(t, err)
}
