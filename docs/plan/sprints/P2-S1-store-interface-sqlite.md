# P2-S1 - Store interface + SQLite implementation

## Goal
Put Phase 1's Postgres code behind a `Store` interface without changing behavior (protected by a fixture end-to-end test in CI), then add the SQLite implementation with FTS5, compact vectors and graph CTEs, both passing one conformance suite.

## Entry criteria
P2-S0 done (D-052, D-056 recorded). Phase 1 tests and CI green.

## Links
[v2-architecture.md#store-interface](../v2-architecture.md#store-interface), [#sqlite-schema](../v2-architecture.md#sqlite-schema), [#compact-vectors](../v2-architecture.md#compact-vectors), [#phase-1-protection](../v2-architecture.md#phase-1-protection); [architecture.md#structural-retriever](../architecture.md#structural-retriever), [#result-contract](../architecture.md#result-contract), [#data-model](../architecture.md#data-model); decisions D-039, D-040, D-052, D-056.

## Tasks

### P2-S1-T01 - Fixture end-to-end test protecting the Phase 1 path
- Why: every refactor in this sprint must be provably behavior-preserving (D-040).
- Depends on: none
- Touches: `testdata/fixture-repo/` (toy Go repo, 6-8 files incl. an interface call, a method chain, a generated file, a test file), `eval/fixture/queries.jsonl` (8 labeled queries across the three routes), `internal/fakeembed/`, `internal/e2e/e2e_test.go`, `internal/e2e/testdata/golden/*.json`, `.github/workflows/ci.yml`
- Details: `fakeembed` is a deterministic `embed.Embedder` (hash of the input text expanded to a unit vector of the registry dimension; no network). The test (guarded by `TEST_DATABASE_URL`) creates a fresh schema, runs walk, parse, graph, chunk, embed against the fixture repo with an in-slice prefix, then runs the harness on the fixture queries for configs `A1`, `A3n` and `B0` and compares the result JSON (per-query ranked files and metrics, without dates or commit ids) with the golden files. Add CI steps: the Postgres integration tests, this e2e, and `go run ./cmd/eval freeze --verify`.
- Acceptance: `TEST_DATABASE_URL=... go test -count=1 ./internal/e2e/...` passes; deleting one golden file makes it fail with a diff; the CI log shows the e2e as PASS (not SKIP); `UPDATE_GOLDEN=1` regenerates goldens and the diff is reviewed.
- Tests: the e2e itself plus a unit test that `fakeembed` is deterministic and unit-norm.
- Out of scope: any refactor; SQLite.
- Owner: agent

### P2-S1-T02 - Define the `Store` interface and the Postgres adapter
- Why: retrievers and stages must stop importing SQL.
- Depends on: P2-S1-T01
- Touches: `internal/store/store.go` (interface), `internal/store/pg/` (adapter wrapping the existing code), `internal/store/storetest/` (conformance suite skeleton)
- Details: derive methods from actual call sites (`grep -rn "pgx" cmd internal`), grouped in sub-interfaces: `Files`, `Symbols`, `Chunks`, `Edges`, `Vectors`, `Summaries`, `Traces`, `Focus`. Vector search is `NearestChunks(ctx, VectorQuery{Model, Kind, Vec, K}) ([]VectorHit, error)` and returns hits without any slice filtering (filtering is the shared helper's job, D-039). The adapter changes no SQL yet; it only moves code behind the methods that the next tasks adopt.
- Acceptance: `go build ./...` and the e2e from P2-S1-T01 still pass; `go doc ./internal/store` lists the sub-interfaces; no package outside `internal/store/pg` is changed except import lines.
- Tests: compile-time assertion `var _ Store = (*pg.Store)(nil)`.
- Out of scope: moving callers (next tasks).
- Owner: agent

### P2-S1-T03 - Move the indexer stages onto the interface
- Why: stages become store-agnostic.
- Depends on: P2-S1-T02
- Touches: `cmd/indexer/*.go`, `internal/walk`, `internal/chunk`, `internal/summarize`, `internal/graph`
- Details: replace direct `pgx` usage in walk, parse, graph, chunk, embed and summarize with `Store` methods, keeping transactions via a `Store.WithTx(ctx, func(Store) error)` helper. No behavior change.
- Acceptance: the e2e passes unchanged (goldens not regenerated); `grep -rln "pgx" cmd/indexer internal/walk internal/chunk internal/summarize internal/graph` returns nothing.
- Tests: existing guarded integration tests still pass.
- Out of scope: retrievers, server.
- Owner: agent

### P2-S1-T04 - Move retrievers, server and eval harness onto the interface
- Why: the shared retriever logic must run on either store.
- Depends on: P2-S1-T03
- Touches: `internal/retrieve/*`, `internal/qa`, `cmd/server`, `cmd/eval`
- Details: lexical, structural, flat/coarse semantic retrievers, the Q&A candidate query, trace writes and eval reads use `Store` methods only. SQL moves to `internal/store/pg`. The slice filter stays in the shared collapse helper (D-039).
- Acceptance: the e2e passes unchanged; `grep -rln "pgx" internal/retrieve internal/qa cmd/server cmd/eval` returns nothing; `go run ./cmd/eval run --config A1 --split dev` on the real database reproduces the stored `eval/results/A1/dev.json` per-query rows exactly (manual check, recorded in Results).
- Tests: existing unit and integration tests; the e2e.
- Out of scope: SQLite.
- Owner: agent

### P2-S1-T05 - SQLite schema, migrations and connection management
- Why: the tool's storage (D-052).
- Depends on: P2-S1-T02
- Touches: `internal/store/sqlite/schema.sql`, `internal/store/sqlite/migrate.go`, `internal/store/sqlite/open.go`
- Details: tables `files, symbols, chunks, edges, embeddings, focus, traces, meta` mirroring Phase 1 columns (types adapted: arrays as JSON text, no vector column type; `embeddings(target_kind, target_id, model, dim, scale REAL, vec BLOB)`), FTS5 table `chunks_fts(tokens)` content-less. Embedded SQL migrations with a `meta.schema_version`; WAL mode, `busy_timeout`, one writer connection. `open(path)` creates the DB and runs migrations idempotently.
- Acceptance: opening a new path creates the schema; opening again is a no-op; `sqlite3 index.db '.schema'` lists all tables and the FTS table; a downgrade attempt (older binary, newer schema) fails with a clear message.
- Tests: unit tests for migration idempotency and version mismatch.
- Out of scope: CRUD methods.
- Owner: agent

### P2-S1-T06 - SQLite store: files, symbols, chunks, edges
- Why: structure persistence.
- Depends on: P2-S1-T05
- Touches: `internal/store/sqlite/{files,symbols,chunks,edges}.go`
- Details: implement the `Files`, `Symbols`, `Chunks`, `Edges` sub-interfaces with batched upserts in transactions, cascade deletes, and `unique(source, caller_id, callee_raw, call_line, coalesce(callee_id,0))` semantics matching Postgres.
- Acceptance: the conformance suite sections for these sub-interfaces (written in this task under `storetest`) pass for both `pg` and `sqlite` (`TEST_DATABASE_URL` set); upserting the same chunk twice leaves one row.
- Tests: `storetest` cases run against both stores.
- Out of scope: FTS, vectors, graph queries.
- Owner: agent

### P2-S1-T07 - SQLite FTS5 with identifier-split tokens
- Why: lexical search parity ([architecture.md#lexical-retriever](../architecture.md#lexical-retriever)).
- Depends on: P2-S1-T06
- Touches: `internal/store/sqlite/fts.go`
- Details: `SetSearchTokens(chunkID, tokens string)` writes `chunk.TSVector(header, content)` output (the same Go splitter as Phase 1) into `chunks_fts`; `SearchLexical(query, limit)` tokenizes the query with `SplitIdentifiers`, runs `MATCH` with `bm25()` ordering, plus exact and prefix match on `symbols.name`. Returns hits (chunk id, score) like the Postgres store.
- Acceptance: `storetest` lexical cases pass on both stores: `schedulePod` finds a chunk containing `schedulePod`; `pod backoff` finds one containing `podBackoff`; ranking order agrees on the exact-identifier case.
- Tests: conformance cases with fixtures.
- Out of scope: ranking tuning.
- Owner: agent

### P2-S1-T08 - SQLite compact vectors and nearest-neighbor search
- Why: int8/256d brute-force search from disk ([v2-architecture.md#compact-vectors](../v2-architecture.md#compact-vectors)).
- Depends on: P2-S1-T06
- Touches: `internal/vec/quantize.go`, `internal/store/sqlite/vectors.go`
- Details: `vec.Truncate(v, n)` (re-normalizes), `vec.QuantizeInt8(v) (q []int8, scale float32)` symmetric per-vector, `vec.DotInt8`. `UpsertEmbedding` stores `dim`, `scale`, BLOB; `NearestChunks` streams rows of one `(model, kind)` with a cursor (no full load), keeps a top-k heap, and returns scores as approximate cosine. A `float32` mode (BLOB of float32, same API) exists for the ablation and is selected by `meta.vector_format`.
- Acceptance: unit test: quantize then dot reproduces the float cosine within 0.01 on 1,000 random unit vectors; `storetest` vector case returns the true nearest neighbor of a planted vector on both stores; peak footprint while searching 100k vectors stays under 100MB of Go heap (`runtime.MemStats.HeapAlloc` sampled during the search in the test).
- Tests: unit tests for quantization; conformance case.
- Out of scope: ANN index (only if P2-S8 shows need).
- Owner: agent

### P2-S1-T09 - SQLite graph queries and focus/trace persistence
- Why: structural retrieval and follow-ups need them.
- Depends on: P2-S1-T06
- Touches: `internal/store/sqlite/graph.go`, `internal/store/sqlite/focus.go`, `internal/store/sqlite/traces.go`
- Details: `Callers(symbolID, depth)` and `Callees` via recursive CTE with a cycle guard (path string), returning rows with hop distance and resolution; same scoring inputs as Postgres. `focus` table `(session_id, seq, kind, ref, created_at)` with push/pop/list methods; `traces` stores JSON payload text.
- Acceptance: `storetest` graph cases (cycle, ambiguous edge, 3 hops) give identical ordered results on both stores; focus push/pop/list passes.
- Tests: conformance cases.
- Out of scope: follow-up rules (P2-S5).
- Owner: agent

### P2-S1-T10 - Conformance suite complete and wired into CI
- Why: both stores must stay equivalent from now on.
- Depends on: P2-S1-T07, P2-S1-T08, P2-S1-T09, P2-S1-T04
- Touches: `internal/store/storetest/`, `.github/workflows/ci.yml`
- Details: finish the suite (`RunAll(t, newStore func() Store)`); the SQLite run needs no services, the Postgres run uses the service container; add an e2e variant that runs the same fixture indexing with the SQLite store and checks that A1's ranked files on the fixture queries equal the Postgres golden (using the same fake embedder).
- Acceptance: `go test -count=1 ./internal/store/...` passes both stores in CI; the SQLite fixture e2e equals the Postgres golden ranked files for the 8 fixture queries.
- Tests: the suite itself.
- Out of scope: performance tuning.
- Owner: agent

## Measurements to record
- Brute-force search latency and heap/footprint for 100k vectors on SQLite (Results).
- Number of conformance cases per store.

## Exit criteria
Postgres path unchanged (fixture e2e goldens untouched, real-DB A1 DEV rows identical); SQLite store passes the conformance suite and the fixture e2e; CI runs all of it; ROADMAP updated.

## Risks + mitigations
- A refactor changes ranking subtly: the goldens and the real-DB A1 DEV check detect it; no golden is regenerated in P2-S1-T02 to T04.
- Pure-Go SQLite slower than expected: P2-S0 numbers set the budget; measured here.

## Demo check
`go test -count=1 ./internal/store/... ./internal/e2e/...` green; `sqlite3` open of a fixture index shows rows.

## Results
(empty)
