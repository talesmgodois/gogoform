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
.PHONY: help dev watch dev-postgres dev-sqlite watch-postgres watch-sqlite keycloak-up keycloak-down db-up db-down db-logs adminer up-sqlite build run test swagger require-postgres \
        setup-tools generate-schema migrate-init migrate-new migrate-up migrate-down migrate-status sqlc build-db

# Connection URLs used by the dev-*/watch-* shortcuts, which pick the engine
# no matter what DATABASE_URL says in .env. The PostgreSQL one follows the
# docker compose credentials (POSTGRES_* in .env). Override either on the CLI,
# e.g. `make dev-sqlite SQLITE_URL=sqlite:/tmp/test.db`.
POSTGRES_URL ?= postgres://$(or $(POSTGRES_USER),postgres):$(or $(POSTGRES_PASSWORD),postgres)@localhost:5432/$(or $(POSTGRES_DB),app_db)?sslmode=disable
SQLITE_URL   ?= sqlite:./data/gogoform.db
# The shortcuts also apply pending migrations at startup (DATABASE_AUTO_MIGRATE),
# so a fresh database works without running dbmate first.
ENGINE_MAKE   = $(MAKE) --no-print-directory DATABASE_AUTO_MIGRATE=true

# AUTH picks how users authenticate, for any target that runs the app (dev,
# watch, run and the dev-*/watch-* shortcuts), overriding AUTH_METHODS from
# .env. It is a comma-separated list of:
#   basic     HTTP Basic (username and password) on every API request
#   jwt       username and password exchanged for a JWT, sent as a Bearer token;
#             also the password form of the /app sign-in page
#   password  basic and jwt (the default when AUTH and AUTH_METHODS are unset)
#   oidc      sign-in through the OIDC provider set by OIDC_* in .env
#   keycloak  oidc against the local Keycloak of docker-compose.yml, started
#             automatically (users alice/alice, bob/bob)
# e.g. `make dev-sqlite AUTH=keycloak`, `make dev-postgres AUTH=jwt,keycloak`.
AUTH_PRESETS := basic jwt password oidc keycloak
comma := ,
empty :=
space := $(empty) $(empty)
KEYCLOAK_PORT ?= 8081
ifneq ($(strip $(AUTH)),)
  AUTH_LIST := $(subst $(comma),$(space),$(AUTH))
  ifneq ($(filter-out $(AUTH_PRESETS),$(AUTH_LIST)),)
    $(error AUTH: unknown value "$(filter-out $(AUTH_PRESETS),$(AUTH_LIST))", expected a comma-separated list of: $(AUTH_PRESETS))
  endif
  override AUTH_METHODS := $(subst $(space),$(comma),$(sort $(patsubst keycloak,oidc,$(patsubst password,basic jwt,$(AUTH_LIST)))))
  export AUTH_METHODS
  ifneq ($(filter keycloak,$(AUTH_LIST)),)
    # The local Keycloak realm (keycloak/realm-gogoform.json).
    override OIDC_ISSUER_URL    := http://localhost:$(KEYCLOAK_PORT)/realms/gogoform
    override OIDC_CLIENT_ID     := gogoform
    override OIDC_CLIENT_SECRET := gogoform-dev-secret
    override OIDC_REDIRECT_URL  := http://localhost:$(or $(SERVER_PORT),8080)/app/oidc/callback
    override OIDC_PROVIDER_NAME := Keycloak
    export OIDC_ISSUER_URL OIDC_CLIENT_ID OIDC_CLIENT_SECRET OIDC_REDIRECT_URL OIDC_PROVIDER_NAME
    KEYCLOAK_DEP := keycloak-up
  endif
endif

# DEV_DEPS: `make dev`/`make watch` only need the docker database for postgres;
# sqlite has no container to start. AUTH=keycloak also starts Keycloak.
DEV_DEPS := $(if $(filter sqlite,$(DB_ENGINE)),,db-up) $(KEYCLOAK_DEP)

help: ## Show commands and descriptions
	@echo "DB_ENGINE=$(DB_ENGINE) (from DATABASE_URL, override with DATABASE_URL=sqlite:... or postgres://...)"
	@awk 'BEGIN {FS = ":.*?## "; printf "\nUsage: make <target>\n\nTargets:\n"} /^[a-zA-Z0-9_-]+:.*?## / {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

dev: $(DEV_DEPS) ## Start the database (postgres only), wait until healthy, then run the app locally
	go run $(PKG)

watch: $(DEV_DEPS) ## Start the database (postgres only), then run the app with hot reload (via air)
	@command -v air >/dev/null 2>&1 || { echo "air not found; run: make setup-tools"; exit 1; }
	air -c .air.toml

dev-postgres: ## Run the app on PostgreSQL (starts the container, applies migrations), whatever .env says
	@$(ENGINE_MAKE) dev DATABASE_URL='$(POSTGRES_URL)'

dev-sqlite: ## Run the app on SQLite (SQLITE_URL, default ./data/gogoform.db, schema created at startup), whatever .env says
	@$(ENGINE_MAKE) dev DATABASE_URL='$(SQLITE_URL)'

watch-postgres: ## Like dev-postgres, with hot reload (via air)
	@$(ENGINE_MAKE) watch DATABASE_URL='$(POSTGRES_URL)'

watch-sqlite: ## Like dev-sqlite, with hot reload (via air)
	@$(ENGINE_MAKE) watch DATABASE_URL='$(SQLITE_URL)'

keycloak-up: ## Start the local Keycloak (realm gogoform, users alice/alice and bob/bob) on KEYCLOAK_PORT (default 8081)
	$(COMPOSE) --profile keycloak up -d --wait keycloak

keycloak-down: ## Stop and remove the local Keycloak container
	$(COMPOSE) --profile keycloak rm -sf keycloak

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

up-sqlite: ## Build and start the API alone on SQLite (compose profile "sqlite"), schema created at startup, on port 8080
	$(COMPOSE) --profile sqlite up -d --build api-sqlite

build: ## Compile the API binary to ./bin/api
	go build -o $(BIN) $(PKG)

run: build $(KEYCLOAK_DEP) ## Build and execute the compiled binary
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
