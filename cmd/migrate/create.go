package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

const defaultMigrationsDir = "internal/storage/migrations"

var migrationName = regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)*$`)

const upTemplate = `-- Write the schema change here. This file runs inside a transaction.
-- Separate statements with a line containing only: --bun:split
-- Do not use the question mark character: bun treats it as a query placeholder.
`

const downTemplate = `-- Write the statements that undo the "up" migration here.
`

func runCreate(args []string, out io.Writer, now func() time.Time) error {
	flags := flag.NewFlagSet("create", flag.ContinueOnError)
	dir := flags.String("dir", defaultMigrationsDir, "directory where the migration files are written")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("create expects exactly one migration name, got %d arguments", flags.NArg())
	}

	paths, err := createMigration(*dir, flags.Arg(0), now())
	if err != nil {
		return err
	}
	for _, path := range paths {
		fmt.Fprintln(out, "created", path)
	}
	return nil
}

// createMigration writes the transactional up and down files of a new migration and returns
// their paths. It never overwrites an existing file.
func createMigration(dir, name string, now time.Time) ([]string, error) {
	if !migrationName.MatchString(name) {
		return nil, fmt.Errorf("invalid migration name %q: use lowercase letters, digits and underscores", name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create directory %s: %w", dir, err)
	}

	base := now.UTC().Format("20060102150405") + "_" + name
	files := []struct {
		suffix  string
		content string
	}{
		{suffix: ".tx.up.sql", content: upTemplate},
		{suffix: ".tx.down.sql", content: downTemplate},
	}

	var paths []string
	for _, f := range files {
		path := filepath.Join(dir, base+f.suffix)
		if err := writeNewFile(path, f.content); err != nil {
			return paths, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func writeNewFile(path, content string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	return file.Close()
}
