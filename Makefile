# Load .env when present (plain KEY=value lines only) and export it to every command.
-include .env
export

GO           ?= go
AIR_VERSION  ?= v1.61.7
LINT_VERSION ?= v2.15.0
MIGRATE      := $(GO) run ./cmd/migrate
GOBIN_DIR    := $(shell $(GO) env GOPATH)/bin

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage: make <target>\n\n"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

##@ Setup
.PHONY: tools
tools: ## Install dev tools (air for hot reload, golangci-lint)
	$(GO) install github.com/air-verse/air@$(AIR_VERSION)
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(LINT_VERSION)
	@echo "Make sure $(GOBIN_DIR) is in your PATH."

.PHONY: deps
deps: ## Download Go dependencies
	$(GO) mod download

.PHONY: tidy
tidy: ## Sync go.mod and go.sum with the imports
	$(GO) mod tidy

##@ Database
.PHONY: db-up
db-up: ## Start Postgres (waits until it is healthy)
	docker compose up -d --wait postgres

.PHONY: db-down
db-down: ## Stop Postgres (keeps the data)
	docker compose down

.PHONY: db-reset
db-reset: ## Destroy the Postgres volume and start from scratch
	docker compose down -v
	$(MAKE) db-up

.PHONY: db-logs
db-logs: ## Follow the Postgres logs
	docker compose logs -f postgres

.PHONY: db-psql
db-psql: ## Open psql inside the Postgres container
	docker compose exec postgres psql -U $${POSTGRES_USER:-impostor} -d $${POSTGRES_DB:-impostor}

.PHONY: migrate-init
migrate-init: ## Create the migration bookkeeping tables (idempotent)
	$(MIGRATE) init

.PHONY: migrate-up
migrate-up: ## Apply all pending migrations
	$(MIGRATE) up

.PHONY: migrate-down
migrate-down: ## Roll back the last group of migrations
	$(MIGRATE) down

.PHONY: migrate-status
migrate-status: ## Show applied and pending migrations
	$(MIGRATE) status

.PHONY: migrate-create
migrate-create: ## Create a new migration: make migrate-create name=add_something
	@test -n "$(name)" || { echo "usage: make migrate-create name=add_something"; exit 1; }
	$(MIGRATE) create $(name)

.PHONY: seed
seed: ## Load themes, words and questions (idempotent). Extra flags: make seed ARGS=-release
	$(GO) run ./cmd/seed $(ARGS)

##@ Run
.PHONY: run
run: ## Run the API once
	$(GO) run ./cmd/api

.PHONY: dev
dev: ## Run the API with hot reload (needs `make tools`)
	@command -v air >/dev/null 2>&1 || { echo "air not found. Run 'make tools' and add $(GOBIN_DIR) to your PATH."; exit 1; }
	air -c .air.toml

.PHONY: build
build: ## Build all binaries into ./bin
	$(GO) build -trimpath -o bin/ ./cmd/...

.PHONY: docker-build
docker-build: ## Build the Docker image
	docker build -t impostor-game:dev .

##@ Quality
.PHONY: test
test: ## Run unit tests
	$(GO) test -race -shuffle=on ./...

.PHONY: test-integration
test-integration: ## Run unit + integration tests (needs `make db-up`)
	$(GO) test -tags=integration -race -p 1 -count=1 ./...

.PHONY: cover
cover: ## Unit test coverage report
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -n 1

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run ./...

.PHONY: fmt
fmt: ## Format the code
	$(GO) fmt ./...

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: clean
clean: ## Remove build output
	rm -rf bin tmp coverage.out
