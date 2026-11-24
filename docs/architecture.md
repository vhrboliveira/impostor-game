# Architecture

## Packages

| Package | Responsibility | Depends on |
|---|---|---|
| `cmd/api` | Composition root: config, logging, database, router, janitor, graceful shutdown | everything below |
| `cmd/migrate`, `cmd/seed` | CLIs for the schema and the content | `storage`, `content`, `config` |
| `internal/config` | Environment variables to a validated `Config` | stdlib |
| `internal/game` | Pure rules: phases, turn ring, vote counting, scoring, rotation. No I/O | stdlib |
| `internal/content` | Embedded catalog and its validation | `game` |
| `internal/storage` | PostgreSQL through bun: pool, models, seed, repositories | `content`, bun |
| `internal/storage/migrations` | Embedded SQL migrations | bun/migrate |
| `internal/httpapi` | REST routes, middleware | stdlib |
| `internal/janitor` | Periodic deletion of expired and empty rooms | stdlib |
| `internal/protocol` | Wire message types (next step) | stdlib |
| `internal/room` | Per-room actor and use cases (next step) | `game`, `protocol` |
| `internal/realtime` | WebSocket transport (next step) | `room`, `protocol`, coder/websocket |

Dependencies point inward. `room` defines the small interfaces it needs (storage, a client that
receives snapshots); `storage` and `realtime` implement them, so `room` never imports them.

## Room actor (next step)

Every active room is owned by one goroutine that reads commands from a channel:

- a player joined or left;
- a message arrived from a player (`vote`, `next_turn`...);
- a timer fired (turn deadline, vote timeout).

Because commands are processed one at a time, room state needs no locks. For each command the
actor applies the rules from `game`, persists the new state, and sends every connected player
their own snapshot (the secret is only included for those who may see it).

A `Manager` maps room codes to actors and creates them on demand. When the server restarts, the
first connection to a room reloads it from PostgreSQL and re-arms the timers from the stored
deadlines.

## Request flow

```
browser -> httpapi (REST: create/join room) -> room.Manager -> storage
browser -> realtime (WebSocket) -> room actor -> game rules, storage
                                       |
                                       +-> snapshots to each connected player
```

## Process lifecycle

`cmd/api` loads the configuration, opens the database (fails fast if it does not answer), starts
the janitor, serves HTTP, and on SIGINT/SIGTERM stops accepting connections and drains for up to
10 seconds. In development `air` sends SIGINT before each restart.

## Scaling

One instance for now. Running several would require sticky routing by room code or a
cross-instance channel (`LISTEN/NOTIFY` is supported by bun's `pgdriver`).
