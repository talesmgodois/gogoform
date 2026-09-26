# Load .env (if present) and export its variables to every recipe.
ifneq (,$(wildcard .env))
include .env
export $(shell sed -n 's/^\([A-Za-z_][A-Za-z0-9_]*\)=.*/\1/p' .env)
endif

BIN        := bin/api
PKG        := ./cmd/api
MAIN       := cmd/api/main.go
COMPOSE    := docker compose

# Database automation (see scripts/setup_migrations.sh). Override on the CLI,
# e.g. `make generate-schema DBML_INPUT=path/to/schema.dbml`.
DB_SCRIPT      := scripts/setup_migrations.sh
DBML_INPUT     ?= ../__ai_work/_inputs/db_schema.dbml
SCHEMA_OUTPUT  ?= ../__ai_work/outputs/schema.sql
DATABASE_URL   ?= postgres://postgres:postgres@localhost:5432/app_db?sslmode=disable
# DB_ENGINE follows DATABASE_URL's scheme: `sqlite:` selects sqlite, anything else postgres.
DB_ENGINE      := $(if $(filter sqlite:%,$(DATABASE_URL)),sqlite,postgres)
MIGRATIONS_DIR ?= db/$(DB_ENGINE)/migrations
DBMATE         := dbmate --migrations-dir $(MIGRATIONS_DIR) --wait --no-dump-schema
export DBML_INPUT SCHEMA_OUTPUT MIGRATIONS_DIR DATABASE_URL
# Tools installed by `make setup-tools` may live outside the user's PATH.
export PATH := $(PATH):$(HOME)/.local/bin:$(shell go env GOPATH 2>/dev/null)/bin

.DEFAULT_GOAL := help
.PHONY: help dev watch db-up db-down db-logs adminer build run test swagger require-postgres \
        setup-tools generate-schema migrate-init migrate-new migrate-up migrate-down migrate-status sqlc build-db

# DEV_DEPS: `make dev`/`make watch` only need the docker database for postgres;
# sqlite has no container to start.
DEV_DEPS := $(if $(filter sqlite,$(DB_ENGINE)),,db-up)

help: ## Show commands and descriptions
	@echo "DB_ENGINE=$(DB_ENGINE) (from DATABASE_URL, override with DATABASE_URL=sqlite:... or postgres://...)"
	@awk 'BEGIN {FS = ":.*?## "; printf "\nUsage: make <target>\n\nTargets:\n"} /^[a-zA-Z0-9_-]+:.*?## / {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

dev: $(DEV_DEPS) ## Start the database (postgres only), wait until healthy, then run the app locally
	go run $(PKG)

watch: $(DEV_DEPS) ## Start the database (postgres only), then run the app with hot reload (via air)
	@command -v air >/dev/null 2>&1 || { echo "air not found; run: make setup-tools"; exit 1; }
	air -c .air.toml

require-postgres:
	@test "$(DB_ENGINE)" = postgres || { echo "ERROR: this target requires PostgreSQL, but DATABASE_URL is sqlite:... (DB_ENGINE=$(DB_ENGINE))" >&2; exit 1; }

db-up: require-postgres ## Start PostgreSQL in the background and wait for its healthcheck (postgres only)
	$(COMPOSE) up -d --wait postgres

db-down: ## Stop and remove the database containers (data volume is kept)
	$(COMPOSE) down

db-logs: require-postgres ## Tail PostgreSQL logs (postgres only)
	$(COMPOSE) logs -f postgres

adminer: require-postgres ## Start Adminer (web UI to query PostgreSQL) on ADMINER_PORT (default 8088) (postgres only)
	$(COMPOSE) up -d --wait adminer

build: ## Compile the API binary to ./bin/api
	go build -o $(BIN) $(PKG)

run: build ## Build and execute the compiled binary
	./$(BIN)

test: ## Run unit/integration tests
	go test -v ./...

swagger: ## Regenerate Swagger documentation into ./docs
	swag init -g $(MAIN) -o ./docs

setup-tools: ## Check for and install dbml2sql, dbmate, sqlc and air
	$(DB_SCRIPT) tools

generate-schema: require-postgres ## Convert the DBML schema into SQL (SCHEMA_OUTPUT) (postgres only)
	$(DB_SCRIPT) schema

migrate-init: require-postgres ## Create the initial dbmate migration from the generated SQL (postgres only, no-op if it exists)
	$(DB_SCRIPT) migration

migrate-new: ## Create an empty dbmate migration in MIGRATIONS_DIR (works for postgres and sqlite): make migrate-new NAME=add_x
	@test -n "$(NAME)" || { echo "usage: make migrate-new NAME=<migration_name>"; exit 1; }
	$(DBMATE) new $(NAME)

migrate-up: ## Apply pending migrations to DATABASE_URL (works for postgres and sqlite)
	$(DBMATE) up

migrate-down: ## Roll back the most recent migration (works for postgres and sqlite)
	$(DBMATE) down

migrate-status: ## Show applied and pending migrations (works for postgres and sqlite)
	$(DBMATE) status

sqlc: ## Generate type-safe Go code for both engines (db/postgres and db/sqlite queries)
	sqlc generate

build-db: ## Full pipeline: generate-schema -> migrate-init -> migrate-up -> sqlc (postgres only; sqlite has no DBML bootstrap)
	$(MAKE) generate-schema
	$(MAKE) migrate-init
	$(MAKE) migrate-up
	$(MAKE) sqlc
