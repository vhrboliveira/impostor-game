package migrations

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var fileName = regexp.MustCompile(`^(\d{14})_([a-z0-9]+(?:_[a-z0-9]+)*)\.tx\.(up|down)\.sql$`)

func TestMigrationFilesFollowTheNamingConvention(t *testing.T) {
	entries, err := fs.ReadDir(sqlFiles, ".")
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	for _, entry := range entries {
		assert.Regexp(t, fileName, entry.Name(), "unexpected migration file name")
	}
}

func TestEveryUpMigrationHasADownMigrationAndViceVersa(t *testing.T) {
	entries, err := fs.ReadDir(sqlFiles, ".")
	require.NoError(t, err)

	files := make(map[string]bool, len(entries))
	for _, entry := range entries {
		files[entry.Name()] = true
	}

	for name := range files {
		switch {
		case strings.HasSuffix(name, ".up.sql"):
			down := strings.TrimSuffix(name, ".up.sql") + ".down.sql"
			assert.True(t, files[down], "%s has no matching down migration", name)
		case strings.HasSuffix(name, ".down.sql"):
			up := strings.TrimSuffix(name, ".down.sql") + ".up.sql"
			assert.True(t, files[up], "%s has no matching up migration", name)
		}
	}
}

func TestMigrationTimestampsAreUnique(t *testing.T) {
	entries, err := fs.ReadDir(sqlFiles, ".")
	require.NoError(t, err)

	seen := make(map[string]string)
	for _, entry := range entries {
		match := fileName.FindStringSubmatch(entry.Name())
		if match == nil || match[3] != "up" {
			continue
		}
		timestamp := match[1]
		previous, duplicated := seen[timestamp]
		assert.False(t, duplicated, "%s and %s share the timestamp %s", previous, entry.Name(), timestamp)
		seen[timestamp] = entry.Name()
	}
}

func TestMigrationsAreNotEmptyAndAvoidBunPlaceholders(t *testing.T) {
	entries, err := fs.ReadDir(sqlFiles, ".")
	require.NoError(t, err)

	for _, entry := range entries {
		content, err := fs.ReadFile(sqlFiles, entry.Name())
		require.NoError(t, err)

		assert.NotEmpty(t, strings.TrimSpace(string(content)), "%s is empty", entry.Name())
		// bun formats "?" as a query placeholder even when no arguments are given.
		assert.NotContains(t, string(content), "?", "%s contains a question mark", entry.Name())
	}
}
