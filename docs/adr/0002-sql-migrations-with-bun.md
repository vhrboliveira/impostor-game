# 2. SQL migrations run by bun's migrator

Status: accepted

## Context
The schema relies on features that do not map well to ORM struct tags: partial unique indexes,
`CHECK` constraints, composite foreign keys and `NULLS NOT DISTINCT`.

## Decision
Write migrations as plain SQL files, embedded in the binary and run by `bun/migrate`.
Files are transactional (`.tx.up.sql` / `.tx.down.sql`).

Queries are the other way around: every table has a bun model (`internal/storage/models.go`)
and every query, in the repositories, the seed and the integration tests, uses the bun query
builder with those models. The models only describe columns; the schema stays in the
migrations. Plain SQL text is limited to the migrations and the test helper that resets the
schema.
A small `cmd/migrate` built on the standard `flag` package exposes `init`, `up`, `down`,
`status` and `create`, instead of pulling in a CLI framework. Enumerations are `text` plus
`CHECK`, not PostgreSQL enums.

## Consequences
The schema is explicit and reviewable as SQL. Migration SQL must avoid the question mark
character (bun treats it as a placeholder). Queries name each column in one place (the models),
take values only as arguments, and read rows straight into structs. A model must list every
column of its table, or reading a whole row (`Returning("*")`, `SELECT *`) fails.
