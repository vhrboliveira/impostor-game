# Impostor Game API: agent guide

This file is the single source of truth about the project for people and AI agents
(`CLAUDE.md` only imports it). Read it before changing anything, and update it in the same
change when a decision changes.

## What this project is

The backend of an in-person party game, played on the phones of everyone in the same place. Each round, all players but one (the impostor) see a secret word. Questions are
read and answered out loud; the app shows who asks whom, keeps a per-round history, runs the
votes and the scoring. The web app (React PWA, mobile first) lives in a separate repository
(`impostor-game-web`) and talks to this API over REST and a per-room WebSocket.

- Go module: `github.com/vhrboliveira/impostor-game`
- The full game specification (in Portuguese) is `docs/specification.md`.

## Language conventions

- **Everything related to code is in English**: identifiers, comments, commit messages, file
  names, documentation under `docs/` (except the specification), REST endpoints and
  WebSocket message types (`/api/room`, `room_snapshot`, `select_theme`...).
- **Only text shown to players is Portuguese (pt-BR)**: themes, words and questions in
  `internal/content/data`. Error responses carry stable English codes
  (`{"code":"room_not_found"}`); the web app translates them.

## Current status

Done: repository, tooling (Makefile, hot reload with air, Docker, CI), configuration, the pure
game rules (`internal/game`), the embedded catalog and its validation (`internal/content`),
the PostgreSQL layer (`internal/storage`: connection, models, seed, room cleanup), the SQL
migrations for the whole schema, the cleanup job (`internal/janitor`), the HTTP server with
health probes and middleware (`internal/httpapi`) and the CLIs (`cmd/api`, `cmd/migrate`,
`cmd/seed`).

Done (slice 1, room lifecycle over REST): `POST /api/room` and `POST /api/room/{code}/join`
(`internal/httpapi/rooms.go`), the room service (`internal/room`: codes, tokens, names, retries)
and its repository (`internal/storage/lobby.go`). Everything compiles and `make lint`,
`make test` and `make test-integration` pass on Go 1.27.4 with PostgreSQL 17.

Done (slice 2, WebSocket and lobby): `GET /ws/room/{code}` (`internal/realtime`), the wire
messages and snapshot (`internal/protocol`), the room `Manager` with one actor per room
(`internal/room/manager.go`, `actor.go`: presence, per-player snapshot, host transfer,
`empty_since`, `start_match`) and its persistence (`internal/storage/live.go`). An end-to-end
test drives REST + WebSocket on a real database (`internal/realtime/e2e_integration_test.go`).

Done (slice 3, round flow up to the voting): theme selection and "Sorteio", drawing the
impostor and the secret with the rotations of `internal/game`, the turn ring with questions
that never repeat, the reveal with "Entendi", turns with deadlines, `+1 min` and automatic
advance, and the timed discussion (`internal/room/round.go`, `snapshot.go`; persistence in
`internal/storage/live.go`). The end-to-end test plays a round from the theme to the voting.

Done (database review): every query of the repositories, the seed and the integration tests
uses the bun models (`internal/storage/models.go`) and the query builder; SQL text is left only
in the migrations.

Done (slice 4, voting, result and end of the match): votes with the 5-minute deadline,
counting and scoring (`game.CountVotes`, `game.ScoreRound`), the impostor's guess in its own
phase (`guess`, 60 seconds, up to 8 options), the result with every vote and the points, the
host's "Próxima rodada" / "Ver pódio", `end_match`, and "play again" (`internal/room/voting.go`;
persistence in `internal/storage/live.go`; migration `add_guess_phase`). The end-to-end test
plays a whole round, ends the match and plays again.

Done (player photos): an optional photo per player (`player_avatars`, migrations
`create_player_avatars` and `raise_player_avatar_limit`), sent over REST (`PUT`/`DELETE /api/room/{code}/me/avatar`, served by
`GET /api/room/{code}/players/{id}/avatar`), validated and re-encoded by `room.NormalizeAvatar`
and applied by the room actor (`internal/room/avatar.go`, `avatar_actor.go`,
`internal/httpapi/avatars.go`, `internal/storage/avatars.go`). The web app shows it as the
thumbnail and in the impostor's reveal.

Done (security hardening, first part of slice 5; from the audit of 2026-11-26): per-client rate
limits with a budget of failed lookups against guessing room codes (`internal/httpapi/ratelimit.go`,
`TRUSTED_PROXIES`), one answer (`401`/`4401`) for an unknown room and a wrong token on the token
endpoints, HTTP read/write timeouts (`cmd/api/server.go`), WebSocket message rate, connection cap
and coalesced snapshots, a `recover` per room actor, rounds frozen once their match ends, `Votes`
allocated for a reloaded voting, timer retries, `empty_since` reconciled at startup, the photo
authenticated before decoding with a cap on JPEG scans, guess options only for the impostor, decoys
from unused words, a weighted impostor/subject draw, seats held only by players who connected,
NFC names without invisible characters, `no-store`/`nosniff` and `application/json` on the REST
API, debug logs refused in prod, CI with read-only permissions, pinned actions and images, and
`govulncheck`.

Done (leaving and vote changes): `leave_room`, `remove_player` and removal after 3 minutes offline
(`internal/room/leave.go`, `players.left_at`, close code `4403`); votes confirmed with a button and
changeable while the voting is open (`CastVote` upserts).

Next step: the rest of slice 5: production origin and CORS, observability and deployment (TLS,
reverse proxy, hosting headers of the web app). Real content (themes, words, questions) is still
to be written.

## Game rules (decided)

### Players and match

- 3 to 20 players per room, exactly one impostor per round.
- The number of rounds is chosen when the room is created: 3, 5, 8, 10 or 12. Match duration is
  irrelevant: no estimate and no warning.
- The host can end the match early (a confirmation step prevents accidents). If the connected
  players drop below 3 during a match, no new round can start (the theme selection needs 3) and
  the host is told they may end it. At the end there is a podium (ties share the position) and
  "play again", which keeps room and players, lets the host choose the number of rounds again
  and starts the scores from zero (a new match in the same room).
- New players can only join in the lobby and between rounds. "Between rounds" means the latest
  round of the match is in its `result` phase. A finished or ended-early match does not accept
  players (`match_finished`); they join once the host's "play again" opens a new match.
- Starting a match needs the host and at least 3 **connected** players.
- If the host leaves or goes offline, the oldest connected player (by join time) becomes the
  host after a 30-second grace period (phones drop the socket whenever the app goes to the
  background). The old host does not get the role back when they return.
- Player names: 1 to 20 printable characters, inner whitespace collapsed, composed to Unicode
  NFC (one spelling per name), without invisible or blank characters (default-ignorable code
  points such as the Hangul fillers, the blank braille pattern) or more than two combining marks
  on a character, first letter upper-cased ("ana" becomes "Ana", so they collide). The rest stays
  as typed, so "ANA" and "Ana" are different. Duplicated names get a number appended ("Ana 2").
- A seat (against the 20) is held by a player who opened the room's WebSocket at least once, or
  who joined less than 2 minutes ago (`game.SeatHoldTime`, `players.connected_at`): joins that
  never open the room cannot keep it full.
  Players reconnect to the same seat with a session token kept on the device (important on
  iOS, which suspends sockets in the background).
- Room code: short (5 characters), no ambiguous letters (O/0, I/1), collision-checked.
- Leaving: a player leaves with "Sair da sala", the host removes someone in the lobby, and a
  player offline for 3 minutes (`room.DefaultOfflineRemoval`) is removed; the app asks for a
  confirmation before leaving or removing. The player is marked (`players.left_at`), never
  deleted (deleting cascades to their rounds): they keep resolving the names of the round
  (`hasLeft` in the snapshot), lose their seat, name and token, and drop out of the scoreboard
  and the podium. Their connections close with `4403`. A host who leaves hands the role to the
  oldest connected player at once. The offline countdown lives in the room actor: when a room
  loads, every player away gets the full 3 minutes.
- Optional player photo (camera or gallery), shown as the thumbnail and revealed with the
  impostor in the result. A player without a photo can send one at any moment (right after
  joining, also between rounds); changing or removing it only in the lobby. The server accepts
  JPEG or PNG up to 768x768 and 500 KiB and stores it re-encoded as a JPEG, without metadata.

### Round flow

1. **Theme**: the host picks the theme at the start of each round from the catalog
   (no preset list; themes can repeat). A "Sorteio" option picks a random theme.
   A theme that cannot feed the round (fewer than 4 words left unused in the room, or fewer
   questions than players) is refused (`theme_unavailable`); "Sorteio" tries the next theme.
2. **Reveal**: each player holds a button to see their role (the secret word, or "you are
   the impostor"). Holding avoids neighbors peeking. Each player taps "Entendi"; the questions
   start when every **connected** player of the round confirmed (offline players never hold
   the round). The host can start without waiting.
   Afterwards, from the questions to the guess, every player of the round can hold "Segure para
   lembrar" to see their role again. The button is the same for everyone (the impostor sees
   "Você é o impostor!"), so its absence never gives the impostor away; the impostor and players
   outside the round never receive the secret from the server, whatever the client does.
3. **Questions**: turn ring (below), questions read aloud, answers spoken (never typed).
4. **Discussion**: free talk for a fixed 3 minutes (`game.DiscussionDuration`); then the voting
   starts by itself. The host can add 1 minute per tap (`game.DiscussionExtension`) or start the
   voting earlier.
5. **Voting**: see below.
6. **Guess**: only when the impostor was found. They pick the secret among up to 8 options
   (words of the theme, or the other players of the round in "players" themes) within 60
   seconds; no pick counts as a wrong guess.
7. **Result**: the impostor, the secret, who voted for whom, the guess and the points of the
   round, with the scoreboard. The host taps "Próxima rodada", which opens the theme selection
   of the next round, or "Ver pódio" after the last round, which ends the match.

### Turn ring

- The players of a round are the ones **connected** when the theme is chosen (at least 3).
  Whoever was offline sits that round out (no turn, never the impostor, no secret) and plays
  the next one.
- The order is shuffled. Each player asks the next one, and the last asks the first. Everyone
  asks once and answers once. The screen of every player shows who asks whom and the question.
- The person who just answered taps "Prosseguir", and becomes the next asker. The host is the
  backup (can advance or skip).
- Default time limit: 3 minutes per turn. The answerer and the host can extend it (+1 minute
  per tap) or advance early. A warning shows 30 seconds before the end, and the turn advances
  by itself when time runs out.
- Questions never repeat within a round. They come from the theme's own questions
  (`questions.theme_id`) plus the generic ones of its kind (`theme_id` null).
- On the last turn the answerer's button reads "Ir para a discussão".

### History

- Every player can navigate back and forth through the turns of the **current round only**,
  never past the current turn. The cursor is local to each player and follows the latest turn
  until the player goes back. During discussion and voting all turns of the round are visible.
- The server only sends turns up to the current one, never future questions.

### Voting and scoring

- Only the players of the round vote, for another player of the round, confirming the choice
  with a button. A player can change their vote (confirming again) while the voting is open; it
  closes as soon as every connected player voted, so the last vote ends the changes.
- Voting closes when all connected players of the round voted or after 5 minutes. Players who
  did not vote, or are offline, count as abstentions (recorded with a null target) and never
  block the round. Until the result only who already voted is public, not for whom.
- The impostor is found only with strictly more votes than any other player. Any tie lets
  the impostor escape. Self-votes are invalid.
- The result shows every vote (who voted for whom) and the points of the round.
- Points: +1 to each player who voted for the impostor (even if the impostor escaped); +2 to
  the impostor when not found; +1 to the impostor when found but guessing the secret.

### Rotation and uniqueness

- Every player of the round can always be drawn as the impostor; players picked less often in
  the match weigh more (`game.PickImpostor`). Results are public, so excluding earlier impostors
  (the old no-repeat cycle) let the players deduce the impostor at the end of each cycle.
- The same word does not repeat within a room.
- "Players" theme: the impostor is drawn first and can never be the subject; every player is
  eligible (the host cannot exclude anyone), weighted toward who was picked less, so the impostor
  cannot deduce the subject from earlier results.
- A words theme feeds a round only with at least 4 unused words in the room
  (`game.MinGuessWords`): the impostor's guess options come from those same unused words, never
  from earlier secrets. The guess options are sent only to the impostor.

### Content

- Themes have a kind: `words` (secret from the word catalog) or `players` (secret is a player
  of the room, with its own questions about habits and personality, always "essa pessoa" to be
  gender-neutral and avoiding appearance, health or sensitive topics).
- Words and questions are limited to 255 characters (`content.MaxTextLength`).
- Questions have a `kind` and an optional `theme_id`: no theme means a generic question that
  fits every theme of the same kind. Each theme needs at least 20 questions available (generic
  plus its own), ideally 30 or more, because a round has one turn per player and questions do
  not repeat. The real content (about 50 to 100 questions: funny, philosophical, and starters
  of "healthy" confusion among friends) is generated during implementation. The current JSON
  files are only a small sample. Only Portuguese for now.

### Retention

- A room is deleted 1 hour after it becomes empty, or 24 hours after creation even if people
  are still in it. Everything else hangs from `rooms` with `ON DELETE CASCADE`.
- A new room starts empty (`empty_since` is its creation time): nobody is connected until the
  first WebSocket opens, so a room nobody opens is deleted after 1 hour.

## Technical decisions

- Go standard library first. The only non-standard dependencies: `uptrace/bun` (ORM and
  migrator, with `pgdialect` and `pgdriver`), `coder/websocket` (the standard library has no
  WebSocket; used from the WebSocket step), `stretchr/testify` (tests) and, when needed,
  `golang.org/x/time/rate` (rate limiting) and `golang.org/x/text/unicode/norm` (NFC of player
  names; it was already in the module graph). Adding any other dependency needs a reason in the
  change description. IDs are generated by PostgreSQL (`gen_random_uuid()`), no UUID library.
- Routing with `net/http` `ServeMux` patterns (`POST /api/room/{code}/join`), logging with
  `log/slog`, configuration only from environment variables (`internal/config`). Middleware:
  request id, access log and panic recovery; the status recorder forwards `Hijack` and `Flush`
  and exposes `Unwrap`, so WebSocket upgrades work through it. `LOG_LEVEL=debug` logs every SQL
  query with its values (`db = db.WithQueryHook(...)`: bun returns a new `*bun.DB`, so reassign
  it), so it is refused with `APP_ENV=prod`. In prod a `DATABASE_URL` without
  `sslmode=verify-full` logs a warning at startup.
- HTTP server (`cmd/api/server.go`): 5 s to read the headers, 30 s for the request and 30 s for
  the response, 2 min idle. Hijacked WebSockets are not affected (net/http clears the deadlines).
- Rate limits (`internal/httpapi/ratelimit.go`, `golang.org/x/time/rate`): per client address
  (`RemoteAddr`, or `X-Forwarded-For` only from `TRUSTED_PROXIES`; IPv6 by /64), a budget for
  creating rooms, one for every join/photo/WebSocket request, and a small one for failed lookups
  (`404`/`401`) of join and the photo endpoints. Values in `docs/protocol.md`. Token endpoints
  answer `401`/`4401` alike for an unknown room and a wrong token, so only join (with the miss
  budget) tells whether a room exists. JSON `POST` bodies must be `application/json`; JSON
  responses carry `Cache-Control: no-store` and `nosniff`.
- PostgreSQL 17 or newer; `storage.Open` refuses older servers (`storage.MinServerVersion`).
  Local dev and the CI both use `postgres:17-alpine`, published on host port **5436** (5432
  inside the container) to avoid clashing with a local PostgreSQL. `.env.example`, the
  integration-test default DSN and the CI service use 5436.
  Moving to an 18+ image needs a different volume mount (`/var/lib/postgresql`) and a
  `pg_upgrade` of the data, so keep Compose and CI on the same major version.
- Player photos: stored in PostgreSQL as `bytea` (not base64) in `player_avatars`, never inside
  the snapshot (it goes to every player on every change). The snapshot carries a versioned URL
  (`?v=<unix ms>`) that browsers cache for good. Validation and re-encoding use only the standard
  library (`image/jpeg`, `image/png`), with the dimensions checked before decoding.
- REST bodies: at most 4 KiB, exactly one JSON value, unknown fields rejected (the photo upload
  is the exception: the raw image, at most 500 KiB). Success of create/join is `201` with
  `{roomCode, playerId, token}`; errors are `{"code": ...}` (full list in `docs/protocol.md`).
- WebSocket: `coder/websocket`. The `Origin` header must be one of `ALLOWED_ORIGINS` (full
  origins such as `http://localhost:5173`), else `403` before the upgrade. The token travels in
  the query string (browsers cannot set headers on the handshake); it is never logged (the
  access log has only the path). Messages are at most 4 KiB. A connection silent for 60 s is
  closed (the web app pings every 25 s). A connection may send 10 messages per second (burst 30),
  else 1008. Close codes: 4401 and 4404 are final; 1001 (shutdown), 1008, 1011
  and 1013 (connection too slow) mean "reconnect".
- Room actors (`room.Manager`): a panic in a command or timer is recovered and stops only that
  room (connections closed with 1011). Created on the first connection by loading the room from the
  database, stopped after 2 minutes without connections, and stopped with every connection
  closed with 4404 when the room reaches `expires_at`. An actor never blocks on a client: each
  connection keeps only its newest unsent snapshot (snapshots are full state) and a 32-message
  buffer for the rest, closed (1013) when that fills up; a player holds at most 4 connections (a
  new one closes the oldest with 1008).
- Linting: golangci-lint v2.15.0 (`LINT_VERSION` in the Makefile and the CI). Older builds cannot
  read Go 1.27 export data.
- Migrations: plain SQL files embedded in the binary and run by bun's migrator
  (`internal/storage/migrations`), transactional (`.tx.up.sql` and `.tx.down.sql`). Raw SQL
  because partial unique indexes, `CHECK`s and composite foreign keys do not fit struct tags.
  `text` + `CHECK` instead of PostgreSQL enums.
- Queries: every table has a bun model in `internal/storage/models.go`, and every query (the
  repositories, the seed and the integration-test fixtures) uses the bun query builder with
  those models (`NewSelect`, `NewInsert`, `NewUpdate`, `NewDelete`, `Relation`, subqueries).
  Hand-written SQL text exists only in the migrations and in the test helper that resets the
  schema. The models describe columns, never the schema; `room` never sees them (`storage`
  converts to the `room` types). Columns with a database default are tagged `nullzero`, so a
  zero value is written as `DEFAULT`; nullable columns are pointers.
- The database enforces invariants whenever it can (see `docs/database.md`).
- Content lives in versioned JSON (`internal/content/data`), loaded by `cmd/seed`
  (idempotent upsert), separate from schema migrations.
- Server authoritative: no rule runs on the client. Secrets (the word, who the impostor is)
  only travel to the players allowed to see them. Session tokens are 32 random bytes,
  stored only as a SHA-256 hash.
- Concurrency: one goroutine (actor) per active room processes commands in order, so there
  are no locks around room state. Hot state is in memory, written through to PostgreSQL, and a
  room is reloaded from the database after a restart. A single server instance for now
  (multiple instances would need sticky routing or `LISTEN/NOTIFY`).
- Graceful shutdown on SIGINT/SIGTERM. Hot reload in development with `air`.

## Repository layout

```
cmd/api            HTTP + WebSocket server (composition root)
cmd/migrate        migrations CLI: init, up, down, status, create
cmd/seed           loads the content catalog
internal/config    environment configuration
internal/protocol  wire message types and the per-player snapshot
internal/game      pure rules, no I/O
internal/room      room service (create/join), Manager and per-room actors
internal/realtime  WebSocket transport (origin, auth, read/write loops)
internal/httpapi   REST, router, middleware
internal/storage   PostgreSQL via bun: db, models, seed, repositories (rooms, live state, photos)
  migrations/      embedded SQL migrations
  storagetest/     fresh migrated database for integration tests
internal/content   embedded catalog (pt-BR) and validation
internal/janitor   expired and empty rooms cleanup
docs/              specification, architecture, database, protocol, adr/
docker/            Postgres init scripts
```

Dependency direction: `httpapi` and `realtime` depend on `room`; `room` depends on `game` and
on small interfaces it defines itself; `storage` implements those interfaces. `game` and
`content` never import I/O packages.

## Commands

`make help` lists everything. The important ones:

- `make tools` installs air and golangci-lint. `make db-up` starts Postgres.
- `make migrate-up | migrate-down | migrate-status | migrate-create name=add_something`
- `make seed`, `make run`, `make dev` (hot reload), `make build`
- `make test` (unit), `make test-integration` (needs `make db-up`), `make lint`, `make fmt`

## Conventions for code changes

- Tests live next to the code they cover (`foo.go` and `foo_test.go`). Integration tests
  end in `_integration_test.go`, start with `//go:build integration` and run with `-p 1`: they
  share one database, whose `public` schema the helper recreates for each test.
- Use `testify` (`require` for preconditions, `assert` for checks), table-driven tests where
  there are several cases, and hand-written fakes instead of mock generators.
- Define small interfaces where they are consumed, accept interfaces and return structs.
- Pass `context.Context` as the first parameter, wrap errors with `%w`, compare with
  `errors.Is` / `errors.As`, no `panic` outside `main` and `init` of embedded registries.
- No package-level mutable state, no `init()` side effects other than registering embedded
  migrations. Inject the clock and randomness (see `game.Rand`) so tests are deterministic.
- Log with `slog` and structured attributes. Never log tokens or secrets.
- Never edit a migration that was already applied anywhere: create a new one. Do not use the
  question mark character in migration SQL (bun treats it as a placeholder). Separate statements
  with a line holding only `--bun:split`. A migration that adds a column also adds the field to
  its model in `internal/storage/models.go` (a model missing a column breaks `Returning("*")`).
- Write queries with the bun query builder and the models, never as SQL strings. Pass values as
  arguments of `Where`/`Set` (`Where("code = ?", code)`), never inside the expression text.
- An insert with `ON CONFLICT ... DO NOTHING` and `Returning` must end with `Scan(ctx)`, which
  returns `sql.ErrNoRows` when the row was skipped; `Exec(ctx)` succeeds silently and leaves the
  id empty. `Count` returns an `int64`.
- Integration tests build their fixtures with the models too (`insert`, `tryInsert`, `count`
  and `exec` in `internal/storage/helpers_integration_test.go`).
- Keep this file and `docs/` updated when a decision changes.

## Security rules

- A player's snapshot never contains the secret of a round in which they are the impostor, nor
  who the impostor is before the result phase, nor future turns or questions.
- Uploaded photos are untrusted: bounded body, dimensions checked before decoding, always
  re-encoded (no metadata, nothing appended survives), served as `image/jpeg` with `nosniff`.
- Validate every message and every REST body. Check the WebSocket `Origin` against
  `ALLOWED_ORIGINS`. Apply rate limits to creating and joining rooms (and to anything that takes a
  room code or a token), and never tell a caller without a valid token whether a room exists.
- Bound every unit of work an untrusted request can start: body size and read time, decoding cost
  (the JPEG scan cap), concurrency, connections per player, messages per connection.
- The impostor and the subject must not be deducible from public results (weighted draws), and
  guess options go only to the impostor.
- The integration-test helper wipes the schema of `TEST_DATABASE_URL`; it refuses names that
  do not end in `_test`.

## Open items

- `players.last_seen_at` is set on joining and never updated (presence lives in the actor).
- Reveal confirmations ("Entendi") live in the actor's memory: if the room is reloaded during a
  reveal, players confirm again.
- Real content: themes, words and 50 to 100 questions (`make seed ARGS=-release` enforces the
  minimums; see `cmd/seed`).
- Production deployment, observability. The reverse proxy must not log the query string of
  `/ws/` (the token travels there) and must set `X-Forwarded-For` (with `TRUSTED_PROXIES`).
- CORS in production: undecided; it depends on whether the web app is served from another origin
  than the API.
