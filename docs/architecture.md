# Architecture

## Packages

| Package | Responsibility | Depends on |
|---|---|---|
| `cmd/api` | Composition root: config, logging, database, room service and manager, router, janitor, graceful shutdown | everything below |
| `cmd/migrate`, `cmd/seed` | CLIs for the schema and the content | `storage`, `content`, `config` |
| `internal/config` | Environment variables to a validated `Config` | stdlib |
| `internal/game` | Pure rules: phases, turn ring, vote counting, scoring, rotation. No I/O | stdlib |
| `internal/content` | Embedded catalog and its validation | `game` |
| `internal/storage` | PostgreSQL through bun: pool, models, seed, repositories (implements `room.Store` and `room.LiveStore`) | `content`, `game`, `room`, bun |
| `internal/storage/migrations` | Embedded SQL migrations | bun/migrate |
| `internal/storage/storagetest` | Fresh migrated database for integration tests (refuses names without `_test`) | `storage`, `migrations` |
| `internal/httpapi` | REST routes (health, create/join room, photos), middleware, per-client rate limits | `room`, `x/time/rate` |
| `internal/janitor` | Periodic deletion of expired and empty rooms | stdlib |
| `internal/protocol` | Wire messages: client message parsing and validation, server messages, the snapshot | stdlib |
| `internal/room` | Room lifecycle service (create/join); `Manager` and one actor per active room | `game`, `protocol` |
| `internal/realtime` | WebSocket transport: origin check, upgrade, read and write loops | `room`, `protocol`, coder/websocket |

Dependencies point inward. `room` defines the small interfaces it needs (storage, a client that
receives snapshots); `storage` and `realtime` implement them, so `room` never imports them.

## Room lifecycle (REST)

`room.Service` creates and joins rooms. It validates the input (name, rounds, code), generates
the room code and the session token, and calls `room.Store`, which `storage.Rooms` implements:

- `CreateRoom` inserts the room, its first match (`lobby`) and the host in one transaction. A
  code collision returns `ErrCodeTaken` and the service retries with a new code (up to 10 times).
- `AddPlayer` locks the room row (`SELECT ... FOR UPDATE`), so concurrent joins to the same room
  run one after the other and the 20-player limit holds. A name collision returns
  `ErrNameTaken` and the service retries with `"Ana 2"`, `"Ana 3"`... (up to 50 times).

`httpapi` decodes the body, calls the service and maps its errors to stable codes
(`docs/protocol.md`). The service never sees HTTP, and the handlers never see SQL.

## Room actors

Every active room is owned by one goroutine (`room/actor.go`) that runs commands from an
unbuffered channel, one at a time, so the room state needs no locks. Commands are closures:

- a connection opened or closed (`Manager.Connect`, `Conn.Leave`);
- a message arrived from a player (`Conn.Handle`: `start_match`, `select_theme`, `next_turn`...);
- a timer fired (host grace period, offline removal after 3 minutes, idle timeout, room expiry,
  turn deadline, end of the
  discussion, end of the voting, end of the guess);
- a player's photo changed over REST (`Manager.SetAvatar`, `Manager.DeleteAvatar`).

For each command the actor applies the rules, writes the change to PostgreSQL through
`room.LiveStore` (each call bounded by 5 seconds), and sends every connected player their own
snapshot (`room/snapshot.go`; secrets only for those who may see them).

`room.Manager` maps room codes to actors. The first connection to a room loads it from the
database (`LiveStore.LoadRoom`) and starts the actor; later connections reuse it. A player who
joined over REST after the actor loaded is picked up by re-reading the players when they connect.

The round flow lives in `room/round.go` (theme, reveal, questions, discussion) and
`room/voting.go` (voting, guess, result, next round, end of the match, play again). Counting and
scoring use `game.CountVotes` and `game.ScoreRound`. Drawing a round uses the pure rules of `game`
(`PickImpostor`, `PickSubject`, `BuildTurnRing`) with a per-actor random generator (injected in
tests); the candidates (unused words, questions) come from `LiveStore.LoadRoundCandidates`, and
the plan is written with `BeginRound`. Each phase change is written first and applied to memory
only when the write succeeded, so a failed write leaves the room as it was. One `phaseTimer` holds
the round's deadline (the turn in progress, or the end of the discussion, the voting or the
guess); extending re-arms it. A room loaded from the database re-arms that timer from the stored
deadline (and a deadline already past fires at once). Timer callbacks act only on the round (and
turn, and phase) they were armed for, and a timer-driven transition whose write fails is retried 5
seconds later. Rounds are bound to their match: once the match finished or ended early
(`actor.activeRound` is nil), no command, presence change or timer of that round runs, and "play
again" cancels the round's timer.

Player photos (`room/avatar.go`, `room/avatar_actor.go`): the HTTP handler reads the body and
calls `Manager.SetAvatar`, which authenticates the token first, then validates and re-encodes the
image (`NormalizeAvatar`, standard library only, outside the actor, at most two at a time, giving
up when the request goes away), and runs the change on the room's actor, starting one if nobody is connected (photos are usually sent before the WebSocket opens).
The actor applies the rule (a first photo at any moment, a change only in the lobby), writes it
(`LiveStore.SetAvatar`), keeps only the version (`PlayerState.AvatarAt`) and broadcasts. Snapshots
carry the versioned URL, never the image; `Manager.Avatar` serves the image straight from the
database.

Lifecycle:

- An actor stops 2 minutes after its last connection closes and is removed from the manager.
  A connection that arrives while it stops gets a fresh actor (the room is loaded again).
- At `expires_at` the actor closes every connection with 4404 and stops. It does the same when it
  learns the janitor deleted the room under it (`SetEmptySince` finds no row).
- The actors are keyed by room code but bound to the room id: a code is reused only after its room
  was deleted, and an actor still running for the old room is expired instead of serving the new
  one.
- `empty_since` follows the real occupancy: every connection clears it (retrying a failed clear),
  the last disconnection sets it, an actor that stops while idle sets it if it was still clear,
  and `cmd/api` marks every room empty at startup (`Rooms.MarkAllEmpty`), since no WebSocket
  survives a restart of the single process.
- A panic inside one actor (a command or a timer) is recovered: it is logged with its stack, the
  room's connections are closed with 1011 and the actor stops, so the process and the other rooms
  keep running. Reconnecting loads the room again.
- Timers are generation-checked: a callback that fires after its timer was cancelled or re-armed
  does nothing. The clock is injected (`room.Clock`) so tests drive the timers.

Clients (`room.Client`) never block the actor. Snapshots are full state, so `realtime` keeps only
the newest unsent one per connection (a burst of changes replaces it instead of piling up); other
messages (errors, pongs) go through a 32-message buffer that closes the connection (1013) when
full. A player holds at most 4 connections; a new one closes the oldest (1008).

## WebSocket connections

`realtime.Handler` checks the `Origin`, upgrades, and calls `Manager.Connect` within 10 seconds
(which hashes the token and authenticates through `LiveStore.Authenticate`; an unknown room and a
wrong token both close with 4401). Then each connection runs two loops:

- the read loop parses messages (`protocol.ParseClientMessage`), answers `ping` itself and passes
  the rest to the actor; it gives up after 60 seconds of silence and closes a connection that
  sends faster than 10 messages per second (burst 30) with 1008;
- the write loop encodes queued messages and performs the close handshakes the actor asks for
  (4404 on expiry, 1013 when slow) and the 1001 of a shutdown.

## Request flow

```
browser -> httpapi (REST: create/join room) -> room.Service -> room.Store (storage)
browser -> realtime (WebSocket) -> room.Manager -> room actor -> game rules, room.LiveStore (storage)
                                       |
                                       +-> snapshots to each connected player
```

## Process lifecycle

`cmd/api` loads the configuration, opens the database (fails fast if it does not answer), marks
every room empty (`MarkAllEmpty`), starts the janitor (batches of 500 rooms, 30 seconds per run),
serves HTTP (`cmd/api/server.go`: 5 s for the headers, 30 s to read a request and 30 s to write
the response, 2 min idle; hijacked WebSockets are not affected), and on SIGINT/SIGTERM: closes every WebSocket with 1001 (clients
reconnect to the next process), stops accepting connections, and waits up to 10 seconds for HTTP
requests and WebSocket handlers to finish (`http.Server.Shutdown` does not track hijacked
connections, so `realtime.Handler.Wait` does). In development `air` sends SIGINT before each
restart.

## Scaling

One instance for now. Running several would require sticky routing by room code or a
cross-instance channel (`LISTEN/NOTIFY` is supported by bun's `pgdriver`).
