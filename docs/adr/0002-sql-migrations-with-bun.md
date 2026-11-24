# 2. SQL migrations run by bun's migrator

Status: accepted

## Context
The schema relies on features that do not map well to ORM struct tags: partial unique indexes,
`CHECK` constraints, composite foreign keys and `NULLS NOT DISTINCT`.

## Decision
Write migrations as plain SQL files, embedded in the binary and run by `bun/migrate`.
Files are transactional (`.tx.up.sql` / `.tx.down.sql`). bun models are used only for queries.
A small `cmd/migrate` built on the standard `flag` package exposes `init`, `up`, `down`,
`status` and `create`, instead of pulling in a CLI framework. Enumerations are `text` plus
`CHECK`, not PostgreSQL enums.

## Consequences
The schema is explicit and reviewable as SQL. Migration SQL must avoid the question mark
character (bun treats it as a placeholder).
