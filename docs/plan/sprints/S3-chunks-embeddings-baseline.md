# S3 - Chunks, embeddings, lexical text, baseline

## Goal
Chunk the filtered repo with context headers, populate `search_tsv` with identifier splitting, wire the existing embedding registry into the embed stage, build per-model HNSW indexes, and ship a minimal CLI search for A0 and A1 with the ANN overlap gate.

## Entry criteria
S1 and S2 done (symbols, edges). Function-summary run is in progress or finished ([S1 overnight procedure](S1-corpus-symbols.md#overnight-run-procedure)). The existing prefix registry, L2 normalization and probe are NOT rebuilt here.
Checklist before S3-T01: decide the chunk ceiling given the nomic embedding `num_ctx` of 2048 (D-003 outcome, S0-12): a request may be at most 0.8 x 2048 = 1,638 estimated tokens including header, title and prefix, so a 1,500-token chunk plus header does not fit; choose one limit for all models or one per model, and record it in D-003.

## Links
[architecture.md#stage-chunk](../architecture.md#stage-chunk), [#stage-embed](../architecture.md#stage-embed), [#embedding-variants](../architecture.md#embedding-variants), [#vector-indexes](../architecture.md#vector-indexes), [#result-contract](../architecture.md#result-contract), [#ollama-requests](../architecture.md#ollama-requests), [#token-estimate](../architecture.md#token-estimate); [eval-protocol.md#ann-overlap](../eval-protocol.md#ann-overlap). Decisions: D-015, D-027, D-029, D-039.

## Run coordination (summary run vs embedding)
Both jobs use Ollama on one GPU; running together slows both and can thrash memory. Rule: the summary run keeps running while building S3 code. Pause it ([S1 procedure](S1-corpus-symbols.md#overnight-run-procedure), step 2) before S3-T09 (embedding runs) and resume it right after each embedding batch finishes. Embeddings cover the slice only (D-029), so there is one embedding batch and therefore one pause.

## Tasks

### S3-T01 - Chunker: per-function chunks and splitting
- Why: chunks are the unit of retrieval for the baseline.
- Depends on: none
- Touches: `internal/chunk/tokens.go`, `internal/chunk/chunk.go`
- Details: `Estimate(s string) int = ceil(len(s)/4)` ([#token-estimate](../architecture.md#token-estimate)). `Split(sym Symbol, src []byte) []Part`: one part if estimate <= 1500; otherwise split at top-level statement boundaries of the function body (tree-sitter `block` direct children), greedy-fill each part to <= 1500 estimated tokens, every part prefixed with the signature line plus `// ... part i/n`. A single statement over 1500 tokens becomes its own part (never split mid-statement; log it). `part_idx` starts at 0. Each part carries `StartLine` and `EndLine` (the source lines it covers; the synthetic signature and `// ... part i/n` lines are not counted).
- Acceptance: `go test -count=1 ./internal/chunk/...` passes; golden test over a 3000-token fixture function yields parts each <= 1500 (except the oversized-statement case) that all start with the signature, and every part has `function.StartLine <= StartLine <= EndLine <= function.EndLine`.
- Tests: golden files for: small function, long function, function with one giant switch, generic function.
- Out of scope: grouping (T02), headers (T03), DB (T06).
- Owner: agent

### S3-T02 - Chunker: grouping small declarations
- Why: a struct and its constructor are far more useful together.
- Depends on: T01
- Touches: `internal/chunk/group.go`
- Details: group a struct/interface symbol with a function in the SAME FILE and package named `New<Type>` or `new<Type>` (or whose single return type is `*Type`/`Type`) when the combined estimate <= 800 tokens: one chunk with both, `symbol_id` = the type's symbol, `StartLine`/`EndLine` = the span of both declarations. A constructor in another file is never grouped. Structs/interfaces without such a constructor are their own chunk (signature + doc + body text up to 1500 tokens). A grouped constructor appears only in the group chunk, never also as its own chunk.
- Acceptance: golden test: struct+constructor -> one chunk; struct without constructor -> one chunk; constructor > 800 combined -> separate chunks; constructor in another file -> separate chunks; no function appears in two chunks (`select symbol_id, count(*) from chunks where part_idx=0 group by 1 having count(*)>1` returns 0 after T06).
- Tests: golden fixtures for the three cases.
- Out of scope: const/var blocks (v1 limitation).
- Owner: agent

### S3-T03 - Context header builder
- Why: the A1 ablation; also supplies lexical text.
- Depends on: T01
- Touches: `internal/chunk/header.go`
- Details: header text, exact format: `// package: <import path>\n// file: <path>\n// signature: <signature>\n// calls: <up to 5 callees>\n// called by: <up to 5 callers>\n`. Callees/callers from `edges` (`source='treesitter'`), ranking: resolution exact first, then name_only, ambiguous excluded, ties by `qualified_name`; names rendered as short qualified names (`Scheduler.schedulePod`). Empty lists print `none`. Deterministic.
- Acceptance: golden test for a symbol with 7 callees prints exactly the top 5 in the specified order; unit test of ranking with mixed resolutions.
- Tests: golden header text; ranking unit tests.
- Out of scope: embedding.
- Owner: agent

### S3-T04 - Migration 00005: embeddings and chunk markers
- Why: idempotent embedding and variants.
- Depends on: none
- Touches: `internal/store/migrations/00005_embeddings_columns.sql`
- Details: `ALTER TABLE embeddings ADD COLUMN input_hash text NOT NULL DEFAULT ''; CREATE UNIQUE INDEX embeddings_uniq ON embeddings (target_kind, target_id, model); ALTER TABLE files ADD COLUMN chunked_sha text; ALTER TABLE chunks ADD COLUMN start_line integer NOT NULL DEFAULT 0, ADD COLUMN end_line integer NOT NULL DEFAULT 0; CREATE TABLE embed_failures (target_kind text NOT NULL, target_id bigint NOT NULL, model text NOT NULL, reason text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (target_kind, target_id, model));` Down reverses.
- Acceptance: migrate, `\d embeddings` shows the column and unique index; inserting a duplicate `(kind,id,model)` fails; `\d chunks` shows `start_line` and `end_line`; `\d embed_failures` exists; `make migrate-down migrate-up` exits 0 and `\d embeddings` is unchanged.
- Tests: migration integration test extension.
- Out of scope: HNSW indexes (T08).
- Owner: agent

### S3-T05 - Identifier splitter and tsvector builder
- Why: lexical search over code identifiers (D-004 fix, audit finding 1).
- Depends on: none
- Touches: `internal/chunk/split.go`
- Details: `SplitIdentifiers(s string) []string`: split camelCase and snake_case, digits and acronym boundaries (`HTTPServer` -> `http`, `server`; `podBackoff_v2` -> `pod`, `backoff`, `v`, `2`... keep tokens of length >= 2 and digit runs), keep the lowercased original too. `TSVector(header, content string) string` returns a string suitable for `to_tsvector('simple', $1)` (space-joined tokens: originals + parts) so SQL does `search_tsv = to_tsvector('simple', $1)`. Also split on `_ . / : -` and whitespace (so package paths in headers yield searchable parts). Postgres `simple` parsing will split some originals further; that is intended and the acceptance tests check the splitter output, not lexemes. Query side reuses `SplitIdentifiers`.
- Acceptance: table test passes: `schedulePod` -> contains `schedulepod`, `schedule`, `pod`; `HTTPServer` -> `httpserver`, `http`, `server`; `pod_backoff` -> `pod_backoff`, `pod`, `backoff`; unicode and empty input do not panic; fuzz test `go test -fuzz=FuzzSplit -fuzztime=20s` finds no panic.
- Tests: table + fuzz.
- Out of scope: stemming.
- Owner: agent

### S3-T06 - `indexer chunk` command
- Why: stage wiring with idempotency.
- Depends on: T02, T03, T04, T05
- Touches: `cmd/indexer/chunk.go`, `cmd/indexer/main.go`
- Details: files where `chunked_sha IS DISTINCT FROM sha` AND `graphed_sha = sha` (needs edges). Per file transaction: build chunks (T01/T02), headers (T03), `content_hash = sha256(header|content)`, `token_count` = estimate of the content only (part prefix lines included, header excluded), `start_line`/`end_line` from the parts, `search_tsv = to_tsvector('simple', <TSVector>)`. Upsert on `(symbol_id, part_idx)`; delete chunks for parts that no longer exist. Prints chunks total, in slice / outside, parts split, oversized-statement count, total tokens, p50/p95 tokens. SIGINT-safe.
- Acceptance: run twice, second prints `0 files to chunk`; `select count(*) from chunks where search_tsv = ''` is 0; `select count(*) from chunks where token_count > 1500` equals the oversized-statement count printed; INT-and-rerun equals an uninterrupted run's chunk count; `select count(*) from chunks where start_line<=0 or end_line<start_line` returns 0; for 20 seeded chunks a script confirms the file lines `start_line..end_line` contain the chunk's first statement.
- Tests: guarded integration test on a fixture tree (2 files, one long function) checking rows, `search_tsv @@ to_tsquery('simple','pod')`, idempotency.
- Out of scope: embedding.
- Owner: agent

### S3-T07 - `indexer embed` command
- Why: wires the existing registry/normalization/probe into the pipeline (does not rebuild them).
- Depends on: T04, T06
- Touches: `cmd/indexer/embed.go`, `internal/embed/` (only batching helpers), `cmd/indexer/main.go`
- Details: flags `--model` (default `OLLAMA_EMBED_MODEL`), `--kinds chunk_raw,chunk_header`, Startup: unknown model = error (existing); run the probe and abort on failure. Work list = in-slice chunks without an `embeddings` row for `(kind,id,model)` or with a different `input_hash`; order by `chunks.id`. Document text: `chunk_raw` = content; `chunk_header` = header+content; title = `<file path> <qualified symbol name>`. Batches of 32 via `Embedder.EmbedDocuments` with explicit `num_ctx` (4096 unless D-003 says lower) and `truncate:false`. Apply [#ollama-requests](../architecture.md#ollama-requests): an item estimated above 0.8 x `num_ctx` is not sent (`too_large`); an input-too-long error is `truncated`; any flagged item makes its batch fall back to one request per item; flagged items are not stored; each is upserted into `embed_failures` (reason `too_large` or `truncated`), skipped on later runs unless `--retry-failed`, and the count is printed. Insert with `ON CONFLICT (target_kind, target_id, model) DO UPDATE`. Progress line every 500 items with items/s and ETA.
- Acceptance: an item estimated above 0.8 x `num_ctx` is not sent (the test server records 0 requests for it) and gets an `embed_failures` row with reason `too_large`; `indexer embed --kinds chunk_raw --limit 100` inserts 100 rows with `vector_dims(vec) = 1024` for qwen3; rerun inserts 0; `select max(abs(vector_norm(vec)-1)) from embeddings` < 0.01; the work list never contains a chunk whose file has `in_slice = false` (`select count(*) from embeddings e join chunks c on c.id=e.target_id join files f on f.id=c.file_id where e.target_kind like 'chunk_%' and not f.in_slice` returns 0).
- Tests: unit tests with `httptest.Server` (batching, `too_large` item never sent, `truncated` error handling and per-item fallback, wrong-dim response rejected); guarded live test.
- Out of scope: summaries (S5), MRL indexes.
- Owner: agent

### S3-T08 - Per-model HNSW index command
- Why: indexed nearest-neighbor search ([#vector-indexes](../architecture.md#vector-indexes)).
- Depends on: T07
- Touches: `cmd/indexer/vindex.go`
- Details: `indexer vindex --model M --kind K [--dim N]` creates `CREATE INDEX IF NOT EXISTS emb_hnsw_<slug>_<kind>_<dim> ON embeddings USING hnsw ((vec::vector(<dim>)) vector_cosine_ops) WHERE model = '<M>' AND target_kind = '<K>' WITH (m=16, ef_construction=128)`. `<slug>` = model name lowercased with `[^a-z0-9]` replaced by `_`; model and kind literals are validated against `embed.KnownModels()` and the kinds enum before being inlined. `<dim>` defaults to the registry dim. Idempotent. Print index size.
- Acceptance: after embedding, `indexer vindex --model qwen3-embedding:0.6b --kind chunk_header` succeeds; `EXPLAIN (SELECT id FROM embeddings WHERE model='qwen3-embedding:0.6b' AND target_kind='chunk_header' ORDER BY vec::vector(1024) <=> $1::vector(1024) LIMIT 10)` shows `Index Scan using emb_hnsw_qwen3_embedding_0_6b_chunk_header_1024`.
- Tests: guarded integration test on a 200-row fixture with `SET enable_seqscan=off` asserting `Index Scan using emb_hnsw_...`, then running the same query 10 times through the pgx pool and asserting the plan is unchanged (partial-index predicate matched, see [#vector-indexes](../architecture.md#vector-indexes)).
- Out of scope: MRL variants (S8).
- Owner: agent

### S3-T09 - Run embedding jobs (operational)
- Why: produces the vectors every later sprint uses.
- Depends on: T08
- Touches: this file (Results)
- Details: PAUSE the summary run (Run coordination above). Embed `chunk_raw` and `chunk_header` for the slice with the default model, build both indexes with `indexer vindex`, then resume the summary run. Record wall time and items/s.
- Acceptance: `select target_kind, count(*) from embeddings where model='qwen3-embedding:0.6b' group by 1` shows `chunk_raw` and `chunk_header` each equal to the slice chunk count minus the `embed_failures` rows of that kind (the failure count is recorded in Results); `make summarize-status` shows the run resumed.
- Tests: none.
- Out of scope: other models (S8).
- Owner: agent

### S3-T10 - Flat semantic retriever and result-contract helper
- Why: A0/A1 and the shared file-collapse helper ([#result-contract](../architecture.md#result-contract)).
- Depends on: T08
- Touches: `internal/retrieve/result.go`, `internal/retrieve/flat.go`
- Details: extend `Result` to `{File string, Score float64, Source Kind, Evidence []Evidence}` (keep existing fields compiling or migrate callers); `CollapseToFiles(hits []Hit, opts CollapseOpts{SliceOnly bool, InSlice func(file string) bool, Limit int}) []Result` applies, in this order: (1) slice filter, (2) max-score per file with all hits kept in `Evidence`, (3) stable sort by score desc then path, (4) cut to `Limit` (D-039). `Flat{variant: raw|header}` embeds the query via `Embedder.EmbedQueries` and runs inside a transaction `SET LOCAL hnsw.ef_search = greatest(<cfg>, 200); SELECT target_id, vec::vector(N) <=> q AS d FROM embeddings WHERE model='<validated literal>' AND target_kind='<validated literal>' ORDER BY vec::vector(N) <=> q LIMIT 200`, joins chunks and files, then calls `CollapseToFiles` with `SliceOnly: true` and `Limit` (default 10). Config `EF_SEARCH` default 200 (HNSW returns at most `ef_search` rows, so it is never below the `LIMIT`). `Evidence` line ranges come from `chunks.start_line/end_line`.
- Acceptance: `go test -count=1 ./internal/retrieve/...` passes; the collapse test shows two chunks from one file produce one result with the max score and both in `Evidence`; a live query returns <= 10 unique in-slice files; an integration test on 300 fixture vectors with `EF_SEARCH=100` still gets 200 raw hits before the collapse; a test proves the slice filter runs BEFORE the dedupe and the top-k cut (fixture: 12 hits where the 10 best are out of slice and 2 are in slice must return those 2, not an empty list).
- Tests: unit tests for `CollapseToFiles` (ties, empty, limit, filter-before-cut); guarded integration test for `Flat` on fixture vectors.
- Out of scope: coarse-to-fine (S5).
- Owner: agent

### S3-T11 - CLI search for A0/A1
- Why: the minimal runnable baseline.
- Depends on: T10
- Touches: `cmd/indexer/search.go` (subcommand `search`), `cmd/indexer/main.go`
- Details: `indexer search --config A0|A1 [--limit 10] "query text"` prints rank, file path, score, best line range and a 1-line snippet. A0 = `Flat{raw}`, A1 = `Flat{header}`. Exit non-zero if embeddings for the config are missing.
- Acceptance: `go run ./cmd/indexer search --config A1 "preempt lower priority pods"` prints 10 in-slice file paths and exits 0; `--config A0` on a DB without `chunk_raw` vectors exits non-zero with a message.
- Tests: unit test for output formatting with a fake retriever.
- Out of scope: JSON output (S4 harness).
- Owner: agent

### S3-T12 - ANN overlap@10 tool
- Why: quality gate before any ablation ([eval-protocol.md#ann-overlap](../eval-protocol.md#ann-overlap)).
- Depends on: T10
- Touches: `cmd/indexer/annoverlap.go` (subcommand `ann-overlap`)
- Details: `indexer ann-overlap --model M --kind K --queries <file|probe> [--ef 200]`. `probe` generates 30 queries (seed 5) from first sentences of doc comments of random in-slice functions (documented in D-027), 12+ words each. For each query compute (a) the Flat top-10 files via HNSW and (b) exact brute force: `ORDER BY vec::vector(N) <=> q` with the index disabled (`SET LOCAL enable_indexscan = off`) over the same slice filter. Overlap@10 per query = |A ∩ B| / 10 on FILES; print mean and min. Both sides use `LIMIT 200` and `hnsw.ef_search = greatest(ef, 200)`; sweep `--ef 200,400,800` (max 1000) when given a list.
- Acceptance: prints mean and min overlap; if mean < 0.95 it exits with status 3 and prints `tune ef_search`; with `--ef 200,400,800` prints a row per value.
- Tests: unit test of the overlap computation; guarded live run.
- Out of scope: DEV-query run (S4-T16), per-model runs (S8).
- Owner: agent

## Measurements to record
In this file's Results and indexing metrics ([eval-protocol.md#indexing-metrics](../eval-protocol.md#indexing-metrics)):
- chunks total/slice, parts split, token p50/p95, oversized statements, truncated embeddings skipped.
- Wall time for chunk; embed items/s and wall time per batch; vector counts per `(model, kind)`; index sizes.
- ANN overlap@10 (mean, min) on the probe set for qwen3, and the `ef_search` chosen; `too_large`/`truncated` counts.

## Exit criteria / Definition of Done
Chunks and `search_tsv` populated (none empty); A0 and A1 searchable via CLI; HNSW indexes used by the planner; overlap >= 95% or `ef_search` tuned to reach it; summary run resumed or finished; ROADMAP updated.

## Risks + mitigations
- Embedding the slice competes with the summary run for the GPU: the pause rule in Run coordination covers it.
- Index build memory: build after loading; `maintenance_work_mem` bump documented in the command output.
- Header too long for the embedder context: the pre-send check skips and counts `too_large` items; if > 1% are flagged, reduce callers/callees to 3 and rerun (record).

## Demo check
`indexer search --config A1 "<natural-language question>"` returns ranked files in under 1s; `indexer ann-overlap --queries probe` prints overlap >= 95%.

## Results
(empty)
