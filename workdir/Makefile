# Load .env (if present) and export its variables to every recipe.
ifneq (,$(wildcard .env))
include .env
export $(shell sed -n 's/^\([A-Za-z_][A-Za-z0-9_]*\)=.*/\1/p' .env)
endif

BIN        := bin/api
MAIN       := cmd/api/main.go
COMPOSE    := docker compose

# Database automation (see scripts/setup_migrations.sh). Override on the CLI,
# e.g. `make generate-schema DBML_INPUT=path/to/schema.dbml`.
DB_SCRIPT      := scripts/setup_migrations.sh
DBML_INPUT     ?= ../__ai_work/_inputs/db_schema.dbml
SCHEMA_OUTPUT  ?= ../__ai_work/outputs/schema.sql
MIGRATIONS_DIR ?= db/migrations
DATABASE_URL   ?= postgres://postgres:postgres@localhost:5432/app_db?sslmode=disable
DBMATE         := dbmate --migrations-dir $(MIGRATIONS_DIR) --wait --no-dump-schema
export DBML_INPUT SCHEMA_OUTPUT MIGRATIONS_DIR DATABASE_URL
# Tools installed by `make setup-tools` may live outside the user's PATH.
export PATH := $(PATH):$(HOME)/.local/bin:$(shell go env GOPATH 2>/dev/null)/bin

.DEFAULT_GOAL := help
.PHONY: help dev db-up db-down db-logs build run test swagger \
        setup-tools generate-schema migrate-init migrate-new migrate-up migrate-down migrate-status sqlc build-db

help: ## Show commands and descriptions
	@awk 'BEGIN {FS = ":.*?## "; printf "Usage: make <target>\n\nTargets:\n"} /^[a-zA-Z0-9_-]+:.*?## / {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

dev: db-up ## Start the database, wait until healthy, then run the app locally
	go run $(MAIN)

db-up: ## Start PostgreSQL in the background and wait for its healthcheck
	$(COMPOSE) up -d --wait postgres

db-down: ## Stop and remove the database containers (data volume is kept)
	$(COMPOSE) down

db-logs: ## Tail PostgreSQL logs
	$(COMPOSE) logs -f postgres

build: ## Compile the API binary to ./bin/api
	go build -o $(BIN) ./cmd/api

run: build ## Build and execute the compiled binary
	./$(BIN)

test: ## Run unit/integration tests
	go test -v ./...

swagger: ## Regenerate Swagger documentation into ./docs
	swag init -g $(MAIN) -o ./docs

setup-tools: ## Check for and install dbml2sql, dbmate and sqlc
	$(DB_SCRIPT) tools

generate-schema: ## Convert the DBML schema into SQL (SCHEMA_OUTPUT)
	$(DB_SCRIPT) schema

migrate-init: ## Create the initial dbmate migration from the generated SQL (no-op if it exists)
	$(DB_SCRIPT) migration

migrate-new: ## Create an empty dbmate migration: make migrate-new NAME=add_x
	@test -n "$(NAME)" || { echo "usage: make migrate-new NAME=<migration_name>"; exit 1; }
	$(DBMATE) new $(NAME)

migrate-up: ## Apply pending migrations to DATABASE_URL
	$(DBMATE) up

migrate-down: ## Roll back the most recent migration
	$(DBMATE) down

migrate-status: ## Show applied and pending migrations
	$(DBMATE) status

sqlc: ## Generate type-safe Go code from db/queries into internal/db
	sqlc generate

build-db: ## Full pipeline: generate-schema -> migrate-init -> migrate-up -> sqlc
	$(MAKE) generate-schema
	$(MAKE) migrate-init
	$(MAKE) migrate-up
	$(MAKE) sqlc
