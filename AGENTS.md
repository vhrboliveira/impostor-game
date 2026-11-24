# Impostor Game API: agent guide
 
This file is the single source of truth about the project for people and AI agents
(`CLAUDE.md` only imports it). Read it before changing anything, and update it in the same
change when a decision changes.
 
## What this project is
 
The backend of an in-person party game, played on the phones of everyone in the same place.
Each round, all players but one (the impostor) see a secret word. Questions are read and
answered out loud; the app shows who asks whom, keeps a per-round history, runs the votes and
the scoring. The web app (React PWA, mobile first) lives in a separate repository
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
 
Next step: the WebSocket step, which implements `internal/protocol`, `internal/room`,
`internal/realtime`, the room REST endpoints and their repositories.
 
The initial code was written without a Go toolchain available. On a machine with Go, run
`go mod tidy`, `make fmt`, `make test` and `make test-integration` first and fix anything they
report.
 
## Game rules (decided)
 
Players and match
- 3 to 20 players per room, exactly one impostor per round.
- The number of rounds is chosen when the room is created: 3, 5, 8, 10 or 12. Match duration is
  irrelevant: no estimate and no warning.
- The host can end the match early (a confirmation step prevents accidents). If the players
  drop below 3 during a match, the host may end it. At the end there is a podium (ties share
  the position) and "play again", which keeps room and players and lets the host choose the
  number of rounds again (a new match in the same room).
- New players can only join in the lobby and between rounds.
- If the host leaves or goes offline, the oldest player becomes the host.
- Duplicated names get a number appended. Players reconnect to the same seat with a session
  token kept on the device (important on iOS, which suspends sockets in the background).
- Room code: short (5 characters), no ambiguous letters (O/0, I/1), collision-checked.
Round flow
1. **Theme**: the host picks the theme at the start of each round from the catalog
   (no preset list; themes can repeat). A "Sorteio" option picks a random theme.
2. **Reveal**: each player holds a button to see their role (the secret word, or "you are
   the impostor"). Holding avoids neighbors peeking.
3. **Questions**: turn ring (below), questions read aloud, answers spoken (never typed).
4. **Discussion**: free talk, optional timer.
5. **Voting**.
6. **Result**: if the impostor was found, they guess the secret from ~8 options (the players'
   names in "players" themes).
Turn ring
- The order is shuffled. Each player asks the next one, and the last asks the first. Everyone
  asks once and answers once. The screen of every player shows who asks whom and the question.
- The person who just answered taps "Prosseguir", and becomes the next asker. The host is the
  backup (can advance or skip).
- Default time limit: 3 minutes per turn. The answerer and the host can extend it (+1 minute
  per tap) or advance early. A warning shows 30 seconds before the end, and the turn advances
  by itself when time runs out.
- Questions never repeat within a round.
History
- Every player can navigate back and forth through the turns of the **current round only**,
  never past the current turn. The cursor is local to each player and follows the latest turn
  until the player goes back. During discussion and voting all turns of the round are visible.
- The server only sends turns up to the current one, never future questions.
Voting and scoring
- Voting closes when all connected players voted or after 5 minutes. Players who did not vote,
  or are offline, count as abstentions and never block the round.
- The impostor is found only with strictly more votes than any other player. Any tie lets
  the impostor escape. Self-votes are invalid.
- Points: +1 to each player who voted for the impostor (even if the impostor escaped); +2 to
  the impostor when not found; +1 to the impostor when found but guessing the secret.
Rotation and uniqueness
- The impostor does not repeat until everyone has been the impostor; then the cycle restarts.
- The same word does not repeat within a room.
- "Players" theme: the impostor is drawn first and can never be the subject; every player is
  eligible (the host cannot exclude anyone) and the same person only comes back after all were
  picked.
Content
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
Retention
- A room is deleted 1 hour after it becomes empty, or 24 hours after creation even if people
  are still in it. Everything else hangs from `rooms` with `ON DELETE CASCADE`.
## Technical decisions
 
- Go standard library first. The only non-standard dependencies: `uptrace/bun` (ORM and
  migrator, with `pgdialect` and `pgdriver`), `coder/websocket` (the standard library has no
  WebSocket; used from the WebSocket step), `stretchr/testify` (tests) and, when needed,
  `golang.org/x/time/rate` (rate limiting). Adding any other dependency needs a reason in the
  change description. IDs are generated by PostgreSQL (`gen_random_uuid()`), no UUID library.
- Routing with `net/http` `ServeMux` patterns (`POST /api/room/{code}/join`), logging with
  `log/slog`, configuration only from environment variables (`internal/config`).
- PostgreSQL 19 (uses `UNIQUE NULLS NOT DISTINCT`). Local dev uses `postgres:19-alpine`,
  published on host port **5436** (5432 inside the container) to avoid clashing with a local
  PostgreSQL. `.env.example`, the integration-test default DSN and the CI service use 5436.
- Migrations: plain SQL files embedded in the binary and run by bun's migrator
  (`internal/storage/migrations`), transactional (`.tx.up.sql` and `.tx.down.sql`). Raw SQL
  because partial unique indexes, `CHECK`s and composite foreign keys do not fit struct tags.
  bun models are only used for queries. `text` + `CHECK` instead of PostgreSQL enums.
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
internal/protocol  wire message types (next step)
internal/game      pure rules, no I/O
internal/room      per-room actor and use cases (next step)
internal/realtime  WebSocket transport (next step)
internal/httpapi   REST, router, middleware
internal/storage   PostgreSQL via bun: db, models, seed, repositories
  migrations/      embedded SQL migrations
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
  end in `_integration_test.go` and start with `//go:build integration`.
- Use `testify` (`require` for preconditions, `assert` for checks), table-driven tests where
  there are several cases, and hand-written fakes instead of mock generators.
- Define small interfaces where they are consumed, accept interfaces and return structs.
- Pass `context.Context` as the first parameter, wrap errors with `%w`, compare with
  `errors.Is` / `errors.As`, no `panic` outside `main` and `init` of embedded registries.
- No package-level mutable state, no `init()` side effects other than registering embedded
  migrations. Inject the clock and randomness (see `game.Rand`) so tests are deterministic.
- Log with `slog` and structured attributes. Never log tokens or secrets.
- Never edit a migration that was already applied anywhere: create a new one. Do not use the
  question mark character in migration SQL (bun treats it as a placeholder).
- Keep this file and `docs/` updated when a decision changes.
## Security rules
 
- A player's snapshot never contains the secret of a round in which they are the impostor, nor
  who the impostor is before the result phase, nor future turns or questions.
- Validate every message and every REST body. Check the WebSocket `Origin` against
  `ALLOWED_ORIGINS`. Apply rate limits to creating and joining rooms.
- The integration-test helper wipes the schema of `TEST_DATABASE_URL`; it refuses names that
  do not end in `_test`.
## Open items
 
- WebSocket step: final message set (`docs/protocol.md`), room actor, REST endpoints
  (`POST /api/room`, `POST /api/room/{code}/join`, `GET /ws/room/{code}`).
- Real content: themes, words and 50 to 100 questions (`make seed ARGS=-release` enforces the
  minimums; see `cmd/seed`).
- Rate limiting, production deployment, observability.
 