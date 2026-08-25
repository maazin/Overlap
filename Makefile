.DEFAULT_GOAL := help
SHELL := /bin/bash

# Pinned rather than @latest so a migration run is reproducible six months from
# now.
#
# Keep GOOSE_VERSION in step with the goose version in api/go.mod. The API
# embeds the migrations and applies them at startup through the same library,
# so these two are the same tool reading the same goose_db_version table. The
# CLI stays here because `create`, `down` and `status` have no server-side
# equivalent and no business being reachable from one.
GOOSE_VERSION := v3.27.3
SQLC_VERSION  := v1.31.1
BIN           := $(CURDIR)/bin
GOOSE         := $(BIN)/goose
SQLC          := $(BIN)/sqlc

DATABASE_URL ?= postgres://overlap:overlap@localhost:5434/overlap?sslmode=disable
MIGRATIONS   := $(CURDIR)/api/migrations

.PHONY: help
help: ## List targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[1m%-16s\033[0m %s\n", $$1, $$2}'

# --- toolchain ---------------------------------------------------------------

$(GOOSE):
	GOBIN=$(BIN) go install github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)

$(SQLC):
	GOBIN=$(BIN) go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

.PHONY: tools
tools: $(GOOSE) $(SQLC) ## Install pinned dev tools into ./bin

# --- database ----------------------------------------------------------------

.PHONY: db-up
db-up: ## Start Postgres and wait until it accepts connections
	docker compose up -d --wait db

.PHONY: db-down
db-down: ## Stop Postgres, keeping the volume
	docker compose down

.PHONY: db-reset
db-reset: ## Destroy the database volume and start clean
	docker compose down -v && $(MAKE) db-up && $(MAKE) migrate

.PHONY: migrate
migrate: $(GOOSE) ## Apply all pending migrations (the API also does this at startup)
	$(GOOSE) -dir $(MIGRATIONS) postgres "$(DATABASE_URL)" up

.PHONY: migrate-down
migrate-down: $(GOOSE) ## Roll back the most recent migration
	$(GOOSE) -dir $(MIGRATIONS) postgres "$(DATABASE_URL)" down

.PHONY: migrate-status
migrate-status: $(GOOSE) ## Show which migrations have run
	$(GOOSE) -dir $(MIGRATIONS) postgres "$(DATABASE_URL)" status

.PHONY: migration
migration: $(GOOSE) ## Create a migration: make migration name=add_events
	@test -n "$(name)" || { echo "usage: make migration name=add_events"; exit 1; }
	$(GOOSE) -dir $(MIGRATIONS) create $(name) sql

# --- api ---------------------------------------------------------------------

.PHONY: api
api: ## Run the API on :8080
	cd api && DATABASE_URL="$(DATABASE_URL)" go run ./cmd/api

.PHONY: test
test: ## Run Go tests with the race detector
	cd api && go test -race ./...

.PHONY: test-integration
test-integration: db-up migrate ## Run tests that need a real Postgres
	cd api && TEST_DATABASE_URL="$(DATABASE_URL)" go test -race -count=1 ./...

.PHONY: sqlc
sqlc: $(SQLC) ## Regenerate typed queries from SQL
	cd api && $(SQLC) generate

.PHONY: check
check: ## Vet, format-check and test
	cd api && go vet ./... && test -z "$$(gofmt -l .)" && go test -race ./...

# --- web ---------------------------------------------------------------------

.PHONY: web
web: ## Run the SvelteKit dev server on :5173
	cd web && npm run dev

.PHONY: web-install
web-install: ## Install frontend dependencies
	cd web && npm install

.PHONY: dev
dev: db-up ## Start Postgres, then the API and web dev servers together
	@$(MAKE) -j2 api web
