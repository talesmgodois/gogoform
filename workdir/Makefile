# Load .env (if present) and export its variables to every recipe.
ifneq (,$(wildcard .env))
include .env
export $(shell sed -n 's/^\([A-Za-z_][A-Za-z0-9_]*\)=.*/\1/p' .env)
endif

BIN        := bin/api
MAIN       := cmd/api/main.go
COMPOSE    := docker compose

.DEFAULT_GOAL := help
.PHONY: help dev db-up db-down db-logs build run test swagger

help: ## Show commands and descriptions
	@awk 'BEGIN {FS = ":.*?## "; printf "Usage: make <target>\n\nTargets:\n"} /^[a-zA-Z0-9_-]+:.*?## / {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

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
