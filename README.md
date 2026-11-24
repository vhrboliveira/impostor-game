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
go mod tidy      # adds the libraries to go.mod and creates go.sum: commit both
make fmt         # normalizes the formatting of the generated files
```
 
`go.mod` starts without versions on purpose: `go mod tidy` resolves them on your machine.
 
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
```
 
With the web app, point its dev proxy to `http://localhost:8080`.
 
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
 
## Troubleshooting
 
- **`air not found`**: run `make tools` and add `$(go env GOPATH)/bin` to your `PATH`.
- **`database "impostor_test" does not exist`**: the init script only runs when the volume is
  created. Run `make db-reset`, or `docker compose exec postgres createdb -U impostor impostor_test`.
- **`port 5436 is already allocated`**: another service uses that port. Change the left side of
  the mapping in `docker-compose.yml` (and the port in `DATABASE_URL` and `TEST_DATABASE_URL`).
- **`syntax error ... NULLS NOT DISTINCT`**: you need PostgreSQL 15 or newer.
- **`missing go.sum entry`**: run `go mod tidy`.
## Documentation
 
- [AGENTS.md](AGENTS.md): project description and all decisions
- [docs/architecture.md](docs/architecture.md): packages and concurrency model
- [docs/database.md](docs/database.md): schema and constraints
- [docs/protocol.md](docs/protocol.md): REST and WebSocket contract (draft)
- [docs/adr](docs/adr): architecture decision records
 