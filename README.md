# Impostor Game API

Backend of an in-person party game played on everyone's phone. Go (standard library first),
PostgreSQL with the bun ORM, REST plus a WebSocket per room.

Project description and every decision: [AGENTS.md](AGENTS.md). Game rules (Portuguese):
[docs/specification.md](docs/specification.md).

## Prerequisites

| Tool | Version | Used for |
|---|---|---|
| Go | 1.27.4 (`go.mod`); the Docker image builds with 1.27.4 | build and run |
| Docker with Compose v2 | recent | local PostgreSQL |
| make | any | task runner |
| git | any | version control |

## Step by step

### 1. Create the repository (first time only)

```bash
cd impostor-game
git init -b main
git add .
git commit -m "chore: initial project structure"

# With the GitHub CLI:
gh repo create vhrboliveira/impostor-game --private --source=. --push

# Or create it on github.com and then:

# git remote add origin git@github.com:vhrboliveira/impostor-game.git

# git push -u origin main
```

### 2. Download the dependencies

```bash
go mod download  # or `go mod tidy` after changing imports: commit go.mod and go.sum together
```

### 3. Configure the environment

```bash
cp .env.example .env
```

The defaults match `docker-compose.yml`. The Makefile loads `.env` (plain `KEY=value` lines).
The application itself only reads real environment variables.

### 4. Install the dev tools

```bash
make tools
```

This installs `air` (hot reload) and `golangci-lint` into `$(go env GOPATH)/bin`. Make sure that
folder is in your `PATH`.

### 5. Start PostgreSQL

```bash
make db-up
```

It waits until the database is healthy. It also creates the `impostor_test` database used by
the integration tests.

PostgreSQL is published on host port **5436** (it is 5432 inside the container), so it does not
clash with a PostgreSQL you may already run locally. `.env.example` already points to 5436.

The image is `postgres:17-alpine`, the same major version as the CI.

### 6. Run the migrations

```bash
make migrate-up
make migrate-status
```

`migrate-up` creates the bookkeeping tables by itself, so `make migrate-init` is optional.

### 7. Load the content

```bash
make seed
```

It is idempotent. The catalog in `internal/content/data` is only a small sample for now.

### 8. Run the API

```bash
make dev     # hot reload: rebuilds and restarts on every .go/.json/.sql change

# or
make run     # plain run
```

Check it:

```bash
curl -i localhost:8080/healthz   # liveness
curl -i localhost:8080/readyz    # readiness (checks the database)

# Create a room (the creator is the host) and join it with its code:
curl -s -X POST localhost:8080/api/room -H 'Content-Type: application/json' \
  -d '{"playerName":"Ana","totalRounds":5}'
# {"roomCode":"K7M2P","playerId":"…","token":"…"}
curl -s -X POST localhost:8080/api/room/K7M2P/join -H 'Content-Type: application/json' \
  -d '{"playerName":"Bia"}'
```

Both return `201` with a session token; errors are `{"code":"room_not_found"}` style
(see [docs/protocol.md](docs/protocol.md)). The JSON endpoints require
`Content-Type: application/json` and are rate-limited per client (`429 rate_limited`; the limits
are in the protocol). A player can then send an optional photo with that
token:

```bash
curl -s -X PUT localhost:8080/api/room/K7M2P/me/avatar \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: image/jpeg' --data-binary @me.jpg
```

With `LOG_LEVEL=debug` every SQL query is logged with its values (photos, token hashes, who the
impostor is), so it is only allowed with `APP_ENV=dev`. A variable set in the shell wins over
`.env` in make targets (`LOG_LEVEL=info make run`).

The web app's dev server (`pnpm dev`, port 5173) proxies `/api` and `/ws` to
`http://localhost:8080`, so run both and open `http://localhost:5173`. Open it in three browser
tabs (or a normal and a private window) to play the lobby with three players.

The WebSocket (`/ws/room/{code}?token=...`) only accepts origins listed in `ALLOWED_ORIGINS`
(default `http://localhost:5173`). To test on phones in the same Wi-Fi, run the web app with
`pnpm dev --host`, open `http://<your-computer-ip>:5173` on the phone and add that origin:

```bash
ALLOWED_ORIGINS=http://localhost:5173,http://[IP_ADDRESS] make dev
```

### 9. Run the tests

```bash
make test               # unit tests
make test-integration   # unit + integration tests (needs make db-up)
make cover              # coverage summary
make lint
```

## Everyday commands

Run `make help` for the full list.

| Command | What it does |
|---|---|
| `make dev` | API with hot reload |
| `make db-up` / `db-down` / `db-reset` | start, stop, or recreate PostgreSQL |
| `make db-psql` | open `psql` in the container |
| `make migrate-up` / `migrate-down` / `migrate-status` | apply, roll back, or inspect migrations |
| `make migrate-create name=add_x` | create a new migration |
| `make seed` | load themes, words and questions |
| `make build` | build all binaries into `./bin` |

## Migrations

Migrations are plain SQL files in `internal/storage/migrations`, embedded in the binary and run
by [bun's migrator](https://bun.uptrace.dev/guide/migrations.html).

Create one:

```bash
make migrate-create name=add_player_avatar
```

This creates a pair of files such as `20261124103000_add_player_avatar.tx.up.sql` and
`.tx.down.sql`. Write the change in the `up` file and how to undo it in the `down` file, then:

```bash
make migrate-up        # apply
make migrate-status    # see what is applied
make migrate-down      # roll back the last group
```

Rules:

- The `.tx.` marker runs each file inside a transaction: PostgreSQL DDL is transactional.
- Separate statements with a line that contains only `--bun:split`.
- Do not use the question mark character (bun treats it as a query placeholder).
- Never edit a migration that was already applied: create a new one.
- A new column needs its field in the model (`internal/storage/models.go`) in the same change:
  queries use the bun models and the query builder, never SQL strings.
- One `migrate up` applies every pending migration as a single group, and `migrate down` rolls
  back that whole group.
- In production, run `/app/migrate up` before starting `/app/api` (both are in the Docker
  image).

## Integration tests

They run against the database in `TEST_DATABASE_URL`. **The helper drops and recreates the
`public` schema on every test**, so the database name must end with `_test` (the helper refuses
anything else). They run with `-p 1` because packages share that one database.

## Docker

```bash
make docker-build
docker run --rm -p 8080:8080 --env-file .env impostor-game:dev
```

The image contains `api`, `migrate` and `seed`. Change the entrypoint to run the others.

Inside a container `localhost` is the container itself, so a `DATABASE_URL` pointing at
`localhost:5436` will not reach the Compose database. Use `host.docker.internal:5436`
(Docker Desktop), or run the container on the Compose network.

The base images are pinned by digest in the `Dockerfile`: when bumping a tag, update its digest
too (`docker buildx imagetools inspect <image:tag>`).

### Production checklist

- `APP_ENV=prod` (it refuses `LOG_LEVEL=debug`), `ALLOWED_ORIGINS` with only the production
  origin, and `DATABASE_URL` with `sslmode=verify-full` (anything else logs a warning).
- Behind a reverse proxy, set `TRUSTED_PROXIES` (its IPs or CIDRs) so the rate limits see the
  real client in `X-Forwarded-For`, and make the proxy append that header. Without it every
  client looks like the proxy and shares one budget.
- The proxy must not log the query string of `/ws/`: the session token travels there.
- Terminate TLS at the proxy (with HSTS) and keep `/readyz` internal.

## Troubleshooting

- **`air not found`**: run `make tools` and add `$(go env GOPATH)/bin` to your `PATH`.
- **`database "impostor_test" does not exist`**: the init script only runs when the volume is
  created. Run `make db-reset`, or `docker compose exec postgres createdb -U impostor impostor_test`.
- **`port 5436 is already allocated`**: another service uses that port. Change the left side of
  the mapping in `docker-compose.yml` (and the port in `DATABASE_URL` and `TEST_DATABASE_URL`).
- **`PostgreSQL 17 or newer is required, the server runs version N`**: the API, `migrate` and
  `seed` refuse older servers. Use the Compose database (`make db-up`) or upgrade yours.
- **`missing go.sum entry`**: run `go mod tidy`.
- **Postgres container restarts with "in 18+, these Docker images are configured to store
  database data in a format..."**: the image was changed to 18 or newer while the volume holds
  PostgreSQL 17 data at `/var/lib/postgresql/data`. Go back to `postgres:17-alpine`, or upgrade
  the data with `pg_upgrade` and mount the volume at `/var/lib/postgresql`.
- **The room screen stays on "Conectando à sala…" and the API logs nothing for `/ws`**: the
  browser's origin is not in `ALLOWED_ORIGINS` (the handshake gets a `403`). See step 8.
- **`golangci-lint` fails with "export data version ... is greater than maximum supported" or
  "used to build golangci-lint is lower than the targeted Go version"**: the linter was built
  with an older Go. Run `make tools` to rebuild it with your Go toolchain.

## Documentation

- [AGENTS.md](AGENTS.md): project description and all decisions
- [docs/architecture.md](docs/architecture.md): packages and concurrency model
- [docs/database.md](docs/database.md): schema and constraints
- [docs/protocol.md](docs/protocol.md): REST contract and WebSocket draft
- [docs/adr](docs/adr): architecture decision records
