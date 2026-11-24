# 1. Standard library first

Status: accepted

## Context
The project is small and long-lived. Every dependency is code to audit, update and understand.

## Decision
Use the Go standard library for everything it covers well: `net/http` with `ServeMux` patterns,
`log/slog`, `encoding/json`, `embed`, `flag`, `crypto/rand`, `context`, `testing` and `httptest`.
Accepted exceptions: `uptrace/bun` (ORM and migrator), `coder/websocket` (no WebSocket in the
standard library), `stretchr/testify` (assertions) and, when needed, `golang.org/x/time/rate`.

## Consequences
Less dependency churn and a smaller attack surface, at the cost of writing a little plumbing
ourselves (middleware, a tiny migrations CLI). A new dependency requires a justification.
