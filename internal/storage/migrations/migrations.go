// Package migrations holds the database schema migrations (plain SQL, embedded) and registers
// them with bun's migrator. Create new ones with `make migrate-create name=...`.
package migrations

import "github.com/uptrace/bun/migrate"

// Migrations is the registry consumed by cmd/migrate and by the integration tests.
var Migrations = migrate.NewMigrations()

func init() {
	if err := Migrations.Discover(sqlFiles); err != nil {
		panic(err)
	}
}
