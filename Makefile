# Load .env (if present) and export its variables to every recipe.
-include .env
export

GOLANGCI_LINT_VERSION := v2.14.0

.PHONY: up down migrate doctor fetch-repo test lint build

up: ## Start Postgres and wait until it is healthy
	docker compose up -d --wait

down: ## Stop Postgres (data is kept in the named volume)
	docker compose down

migrate: ## Apply database migrations
	go run ./cmd/indexer migrate

doctor: ## Check Postgres, extensions, Ollama and models
	go run ./cmd/indexer doctor

fetch-repo: ## Shallow-clone Kubernetes at KUBE_TAG into KUBE_DIR
	go run ./cmd/indexer fetch-repo

test: ## Run tests (Postgres integration test runs only if TEST_DATABASE_URL is set)
	go test ./...

lint: ## Run golangci-lint (brew install golangci-lint)
	@command -v golangci-lint >/dev/null || { echo "golangci-lint not found: brew install golangci-lint (CI uses $(GOLANGCI_LINT_VERSION))"; exit 1; }
	golangci-lint run

build: ## Build all binaries into bin/
	go build -o bin/ ./cmd/...
