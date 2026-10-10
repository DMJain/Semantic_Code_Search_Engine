# Load .env (if present) and export its variables to every recipe.
-include .env
export

GOLANGCI_LINT_VERSION := v2.14.0

.PHONY: up down migrate migrate-up migrate-down reset-db doctor fetch-repo test lint build

up: ## Start Postgres and wait until it is healthy
	docker compose up -d --wait

down: ## Stop Postgres (data is kept in the named volume)
	docker compose down

migrate: ## Apply database migrations
	go run ./cmd/indexer migrate

migrate-up: migrate ## Apply database migrations (alias for `migrate`)

migrate-down: ## Roll back exactly one migration
	go run ./cmd/indexer migrate down

# Fail-closed emptiness guard for reset-db. The SQL runs an EXACT count(*) per
# user table via query_to_xml (not the approximate pg_stat_user_tables.n_live_tup),
# and the wipe is refused unless psql exits 0 AND every non-version table is empty.
# The query's exit status is checked explicitly, so a psql/auth/not-ready error
# can never look like "empty".
reset-db: ## Rebuild the DB from an empty volume; fails closed if the emptiness check cannot run
	docker compose up -d --wait
	@counts=$$(docker compose exec -T postgres psql -tA -v ON_ERROR_STOP=1 \
		-U $${POSTGRES_USER:-codesearch} -d $${POSTGRES_DB:-codesearch} \
		-c "SELECT relname || '=' || (xpath('/row/c/text()', query_to_xml('SELECT count(*) AS c FROM ' || quote_ident(relname), false, true, '')))[1]::text FROM pg_stat_user_tables WHERE relname <> 'goose_db_version' ORDER BY relname"); \
	status=$$?; \
	if [ $$status -ne 0 ]; then \
		echo "refusing to wipe: emptiness check failed (psql exit $$status)"; exit 1; \
	fi; \
	echo "row counts before wipe:"; echo "$$counts"; \
	if echo "$$counts" | grep -qE '=[1-9][0-9]*$$'; then \
		echo "refusing to wipe: a non-version table holds rows (see counts above)"; exit 1; \
	fi
	docker compose down -v
	docker compose up -d --wait
	go run ./cmd/indexer migrate

doctor: ## Check Postgres, extensions, Ollama and models
	go run ./cmd/indexer doctor

fetch-repo: ## Shallow-clone Kubernetes at KUBE_TAG into KUBE_DIR
	go run ./cmd/indexer fetch-repo

test: ## Run every test fresh (uncached); fail if the Postgres integration test is skipped
	@if [ -z "$$TEST_DATABASE_URL" ]; then \
		echo "TEST_DATABASE_URL is not set; refusing to run tests (the Postgres integration test would skip)"; \
		exit 1; \
	fi
	@tmp=$$(mktemp); trap 'rm -f "$$tmp"' EXIT; \
	go test -count=1 -v ./... >"$$tmp" 2>&1; status=$$?; \
	cat "$$tmp"; \
	if [ $$status -ne 0 ]; then exit $$status; fi; \
	if grep -q -- '--- SKIP' "$$tmp"; then \
		echo "ERROR: a test was skipped; tests must run, not skip"; \
		exit 1; \
	fi

lint: ## Run golangci-lint (brew install golangci-lint)
	@command -v golangci-lint >/dev/null || { echo "golangci-lint not found: brew install golangci-lint (CI uses $(GOLANGCI_LINT_VERSION))"; exit 1; }
	golangci-lint run

build: ## Build all binaries into bin/
	go build -o bin/ ./cmd/...
