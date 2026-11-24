# 4. Let the database enforce the invariants

Status: accepted

## Context
Application bugs and concurrent writes can corrupt game data if the only guard is Go code.

## Decision
Encode every invariant that PostgreSQL can express: unique and partial unique indexes,
`CHECK` constraints and composite foreign keys (see `docs/database.md`). Integration tests prove
each one.

## Consequences
Invalid states are rejected at the source. Rules that depend on the application (such as the
secret never reaching the impostor) stay in Go and are covered by unit tests.
