# Semantic Code Search Engine

A semantic code search and Q&A engine over the Kubernetes codebase. An
offline Go indexer parses Kubernetes with tree-sitter, builds a call graph,
summarizes code at function, file and package level with a local LLM, and
embeds chunks and summaries into Postgres (pgvector + pg_trgm + full-text
search). A Go query service routes each question to lexical, structural or
semantic retrievers, fuses their results, and answers through a multi-hop
Q&A loop.

**Status: work in progress.** Only the project skeleton exists so far.

## Setup

Requirements: macOS on Apple Silicon, Go (see `go.mod`), Docker, git.

1. **Install Ollama natively** (not in Docker):

   ```sh
   brew install ollama   # or download from https://ollama.com/download
   ollama serve          # or open the Ollama app
   ```

   Ollama runs outside Docker on purpose: Docker on macOS cannot reach the
   Apple Silicon GPU (Metal), so a containerized Ollama would run on the CPU
   only and be many times slower. `docker-compose.yml` contains Postgres only.

2. **Pull the models:**

   ```sh
   ollama pull qwen2.5-coder:7b
   ollama pull qwen3-embedding:0.6b
   ```

3. **Configure:** `cp .env.example .env`, then set `KUBE_TAG` to a stable
   Kubernetes release tag (e.g. `v1.37.1`). The Makefile loads `.env`.

4. **Start Postgres:** `make up`
5. **Create the schema:** `make migrate`
6. **Check everything:** `make doctor`
7. **Fetch Kubernetes:** `make fetch-repo`

Other targets: `make test`, `make lint` (needs `brew install golangci-lint`),
`make build`, `make down`. To also run the Postgres integration test:

```sh
TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/store/
```

## Architecture

_TODO_

## Design Decisions

_TODO_

## Evaluation

_TODO_

## What I'd Do Differently

_TODO_
