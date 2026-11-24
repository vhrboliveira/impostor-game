package migrations

import "embed"

// sqlFiles holds the SQL migrations embedded in the binary. Files are named
// YYYYMMDDHHMMSS_name.tx.up.sql / .tx.down.sql; the ".tx" marker makes bun run each file inside
// a transaction.
//
//go:embed *.sql
var sqlFiles embed.FS
