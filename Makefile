# Load .env (if present) and export its variables to every recipe.
-include .env
export

GOLANGCI_LINT_VERSION := v2.14.0
# Newest actionlint that builds/runs under GOTOOLCHAIN=local (see S1 Results).
ACTIONLINT_VERSION := v1.7.12
WORKFLOW ?= .github/workflows/ci.yml

.PHONY: up down migrate migrate-up migrate-down reset-db doctor fetch-repo test test-db-clean lint lint-ci ci-local build summarize-run summarize-pause summarize-status verify-slice

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
	REQUIRE_TEST_DB=1 go test -count=1 -v ./... >"$$tmp" 2>&1; status=$$?; \
	cat "$$tmp"; \
	if [ $$status -ne 0 ]; then exit $$status; fi; \
	if grep -q -- '--- SKIP' "$$tmp"; then \
		echo "ERROR: a test was skipped; tests must run, not skip"; \
		exit 1; \
	fi

test-db-clean: ## Drop leftover codesearch_test_* throwaway databases from interrupted test runs
	@for db in $$(docker compose exec -T postgres psql -tA -v ON_ERROR_STOP=1 \
		-U $${POSTGRES_USER:-codesearch} -d postgres \
		-c "SELECT datname FROM pg_database WHERE datname LIKE 'codesearch\\_test\\_%'"); do \
		echo "dropping $$db"; \
		docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U $${POSTGRES_USER:-codesearch} -d postgres \
			-c "DROP DATABASE \"$$db\" WITH (FORCE)" || exit 1; \
	done

lint: ## Run golangci-lint (brew install golangci-lint)
	@command -v golangci-lint >/dev/null || { echo "golangci-lint not found: brew install golangci-lint (CI uses $(GOLANGCI_LINT_VERSION))"; exit 1; }
	golangci-lint run

lint-ci: ## Validate the workflow with a pinned actionlint (no install needed)
	GOTOOLCHAIN=local go run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION) $(WORKFLOW)

ci-local: ## Mirror ci.yml locally against the compose Postgres (no skips allowed)
	CGO_ENABLED=1 go vet ./...
	CGO_ENABLED=1 $(MAKE) test
	$(MAKE) lint
	$(MAKE) lint-ci

build: ## Build all binaries into bin/
	CGO_ENABLED=1 go build -o bin/ ./cmd/...

summarize-run: ## Build the indexer and run the function-summary level keep-awake (concurrency 1)
	go build -o bin/indexer ./cmd/indexer
	caffeinate -i bin/indexer summarize --level function --concurrency 1

summarize-pause: ## Interrupt the running summarize (exit 0 stopped, 2 nothing running, 3 did not exit)
	@sh scripts/summarize-pause.sh

summarize-status: ## Print current-prompt-version summary counts and an ETA
	go run ./cmd/indexer summarize --status

verify-slice: ## Recompute the in-slice path hash and fail on drift (D-070)
	go run ./cmd/indexer walk --verify-slice
