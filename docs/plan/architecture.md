# Architecture (canonical)

Sprint files link to headings here. Anything not stated here is decided in the sprint file that builds it.
Decisions and rationale: `docs/plan/decisions.md` (IDs `D-nnn`). Term definitions: `docs/plan/glossary.md`.

## Corpus and slice

- Corpus: `kubernetes/kubernetes`, shallow clone at one pinned tag (D-006). The clone keeps `vendor/` on disk (needed by `callgraph-gt`); indexes never include it.
- Excluded from ALL indexes: `vendor/`, `zz_generated*`, `*.pb.go`, `testdata/`, `*_test.go`, generated client code under client-go (generated clientsets, listers, informers, applyconfigurations). Only `.go` files are indexed. Extra rule: a file whose first 10 lines contain `Code generated` and `DO NOT EDIT` is excluded. Build context: only files selected by `go/build.Context{GOOS:"linux", GOARCH:"amd64", CgoEnabled:false}.MatchFile` are indexed (rule `build_constraint`); because `MatchFile` does not itself reject `import "C"`, the rule also excludes any file importing C while cgo is off, so results are identical whatever the shell sets. `callgraph-gt` loads packages with the identical context ([#stage-callgraph-gt](#stage-callgraph-gt)).
- Slice = the four roots `pkg/scheduler`, `pkg/kubelet`, `pkg/controller`, `staging/src/k8s.io/client-go` (non-generated), minus the frozen drop list. Final size is set in S1 and never changes (D-005); the freeze (D-070) dropped `staging/src/k8s.io/client-go` to fit the compute budget, so the slice is the three `pkg/...` roots at tag v1.37.0, and its sha256 is in `internal/walk/slice.sha256`. `files.in_slice` marks it.
- Chunks, `search_tsv` and edges cover the whole filtered repo (no GPU needed). Vectors (raw chunks, header chunks, summaries) exist for the slice only (D-029). Baseline and system are always evaluated on the slice.

## Stack

Go only (no Python). Postgres `pgvector/pgvector:pg17` (extensions `vector`, `pg_trgm`), `pgx/v5`, `pgvector-go`, `goose`. Ollama native on macOS (Metal), never in Docker: `qwen2.5-coder:7b` (summaries), `qwen3-embedding:0.6b` (default embedder), comparison candidate `nomic-embed-text` (D-003). Groq 70B-class model for router + Q&A at query time only, Ollama as fallback (D-007). `go-tree-sitter` for parsing. `golang.org/x/tools` only for the ground-truth call graph.

## Interfaces

Already in the repo (provisional structs, sprints may extend): `embed.Embedder` (`EmbedQueries`, `EmbedDocuments`, `Dim`, `Model`), `llm.LLM` (`Complete`), `retrieve.Retriever` (`Retrieve`), `retrieve.Router` (`Route`). Everything swappable or ablatable sits behind one of these.

## Result contract

Every `Retriever` returns `[]Result{File, Score, Source, Evidence[]}` ranked by file (`Source` = the retriever kind). Chunk, symbol and summary hits collapse to files by max score; `Evidence` keeps the underlying hits (kind, id, score, line range from `chunks.start_line/end_line`) for traces and Q&A. One helper in `internal/retrieve` does the collapse: it filters to the slice FIRST, then de-duplicates files, then cuts to top-k (D-039); no retriever reimplements it. Metrics rules: `docs/plan/eval-protocol.md#retrieval-metrics`.

## Data model

Initial schema is `internal/store/migrations/00001_init.sql` (never edited). Later changes are new migrations, listed by the sprint that adds them:
- 00002 (S0): `chunks.search_tsv` becomes a regular `tsvector NOT NULL DEFAULT ''` column populated from Go; GIN index recreated.
- 00003 (S1): `symbols.raw_calls text[]`, `files.parsed_sha text`, `summaries.reason text`, unique index `summaries_uniq (level, target_id, prompt_version)`.
- 00004 (S1): `summaries.completed_at timestamptz` (nullable).
- 00005 (S2): `edges.call_line int`, `edges.call_kind text`, `edges.unresolved_reason text` (external, no_match, unknown_receiver, func_value, candidates_capped), `resolution` gains `unresolved` (D-073), `edges_callee_id_fkey` becomes ON DELETE CASCADE (D-076), `files.graphed_sha text`, `files.graphed_symbols_fp text`, unique edge index.
- 00006 (S3): `embeddings.input_hash text`, unique `(target_kind, target_id, model)`, `files.chunked_sha text`, `chunks.start_line int NOT NULL`, `chunks.end_line int NOT NULL`, table `embed_failures(target_kind, target_id, model, reason, created_at, primary key (target_kind, target_id, model))` (per part; a grouped chunk lives in one file and stores the span of both declarations).
- 00007 (S5): `packages` table.
- 00008 (S9): summaries unique index also includes `model`, so 7B and 3B summary sets coexist.
`embeddings.target_kind` values: `chunk_raw`, `chunk_header`, `summary_function`, `summary_file`, `summary_package`. `embeddings.target_id` is `chunks.id` for the chunk kinds and `summaries.id` for the summary kinds.

## Resumability

Every stage reads/writes Postgres, is idempotent, and resumes after a crash through status columns or content hashes (`files.sha` vs `*_sha`, `summaries.status`+`input_hash`, `embeddings.input_hash`). SIGINT/SIGTERM finishes the in-flight item, commits, exits 0. Each stage is a subcommand of `cmd/indexer` and prints stats.

## Ollama requests

Every Ollama request (summarize and embed) passes an explicit `num_ctx` from per-model config. Two layers guard against silent truncation.

1. Pre-send, deterministic: if the estimated tokens of the whole request ([#token-estimate](#token-estimate)) exceed 0.8 x `num_ctx`, the request is not sent. A summary row is marked `status='failed'` with reason `too_large`; an embed item is not stored and is recorded in `embed_failures` with the same reason (skipped on later runs unless `--retry-failed`). Embedding `num_ctx` is set per model so that the largest chunk (1500 estimated tokens plus header, title and prefix, at most 2000) is under 0.8 x `num_ctx`: 4096 unless the model card maximum is lower (then lower the chunk limit and record it in D-003).
2. Post-send: embed requests set `truncate:false` and an input-too-long error is recorded in `embed_failures` as `truncated`; a generation response with `prompt_eval_count >= num_ctx - num_predict` marks the row `status='failed'`, reason `truncated`. `prompt_eval_count` is NOT compared with the estimate: Ollama's prompt cache can lower it for a repeated prefix, so that comparison gives false failures. S0-T07 sends one request twice and records both counts to confirm. `/api/embed` returns one aggregate `prompt_eval_count` per request, so embed items are sent one per request whenever the pre-send check flags any item in a batch.

## Token estimate

`tokens = ceil(bytes/4)`, one constant in `internal/chunk/tokens.go`, offline and deterministic (D-015).

## Stage: walk

Apply exclusions ([#corpus-and-slice](#corpus-and-slice)), upsert `files(path, package, sha, loc, in_slice)`, delete rows for vanished files, skip unchanged sha. `package` is the Go import path derived from directory (`k8s.io/kubernetes/pkg/scheduler`, staging paths map to `k8s.io/client-go/...`). Files rejected by the build context ([#corpus-and-slice](#corpus-and-slice)) are counted under `build_constraint`. Prints counts by slice/non-slice, LOC, excluded counts per rule.

## Stage: parse

tree-sitter extraction of functions, methods (with receiver), structs, interfaces into `symbols`: `qualified_name` like `k8s.io/kubernetes/pkg/scheduler.(*Scheduler).schedulePod`, line range, signature, doc comment, and `raw_calls` (raw call-expression text such as `sched.cache.AssumePod`, deduplicated, source order). `raw_calls` is used for summarization input only (D-019). `qualified_name` is globally unique: duplicates within a package (for example several `init` functions, or same-named functions in different files) get the suffix `#<n>`, numbered from the second occurrence by `(path, start_line)` over the whole package before insert.

## Stage: graph

Re-parses each file with tree-sitter (D-019) and resolves call expressions to `edges(source='treesitter')`. Needs local variable types and file import aliases. Resolution order: same package (Rule 1), import aliases (Rule 2: a function in the aliased package is `exact`; an import path with no indexed file is `unresolved`/`external`; an indexed package without the function is a declared-type conversion `skipped_conversion` or `unresolved`/`no_match`), receiver types from local declarations. A cross-package declared-name index over every indexed package (built by scanning each file once) decides conversions and external vs no_match. `resolution` in {exact, name_only, ambiguous, unresolved} (D-073): `name_only` is a guess and always has a callee; `unresolved` has no callee and an `unresolved_reason` (external, no_match, unknown_receiver, func_value, candidates_capped). An ambiguous call stores one row per candidate callee, up to a cap (D-075; Rule 4 candidates are concrete types declaring every method name of the interface). `callee_raw` is always stored. Builtin calls and type conversions are not stored (counted). Freshness is by symbol-set fingerprint (D-076). Interface method calls are expected to be mostly ambiguous or capped. `call_kind` is a best-effort syntactic guess (func, concrete_method, interface_method, func_value, unknown).

## Stage: callgraph-gt

Separate command `cmd/callgraph-gt`. Type-checked VTA call graph (D-011) of the SLICE via `go/packages` under the build context of [#corpus-and-slice](#corpus-and-slice) (`GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=local`, workspace load; D-058 amended, D-071); the set of slice `.go` files that `walk` marks equals the set `go/packages` loads for the slice (checked in S2), stored as `edges(source='gopackages')`, `resolution='exact'` (D-077), with `call_kind` from SSA call mode. Evaluation only; never read by the server. The working invocation (env, flags) is recorded in D-025.

## Stage: chunk

One chunk per function/method; chunks over 1500 estimated tokens split at top-level statement boundaries and every part keeps the signature; small related decls grouped (struct + its `NewX` constructor, only when both are in the same file); top-level const/var blocks are not chunked in v1 (limitation). Each chunk stores `start_line` and `end_line`. Each chunk gets a context header: package, file, signature, top 5 callees, top 5 callers (treesitter edges; exact before name_only, ambiguous excluded, ties by name). `search_tsv` is built in Go from header+content with identifiers split (camelCase and snake_case into parts, original kept), lowercased; the same splitter is applied to lexical queries.

## Stage: summarize

Slice only, bottom-up: function (1-2 sentences; prompt = code + signature + doc + RAW call names, D-014) then file (function summaries + top-level decls and type docs) then package (file summaries). Functions with body under 3 lines are skipped; types get no summary of their own. Stores `model`, `prompt_version`, `input_hash`, `status`. Unchanged `input_hash` is skipped. Concurrency 1-2. Long runs survive interruption. Function level is run overnight from S1; file/package levels from S5.

## Stage: embed

Raw chunks and summaries via `Embedder.EmbedDocuments`, title = file path + qualified symbol name (package path for package summaries). The prefix template registry, L2 normalization and probe already exist (verified in S0); this stage wires them, it does not rebuild them. Unknown model is a startup error. The probe (no NaN/Inf, length == `Dim()`, L2 norm ~1) runs in doctor and before every indexing run. Only in-slice chunks and summaries are embedded (D-029).

## Embedding variants

`chunk_raw` = chunk content without header (config A0). `chunk_header` = header + content (A1 onward). Both, and all summary kinds, are embedded for the slice only, for every model that is evaluated (D-029).

## Vector indexes

Vectors are stored full-dimension in `embeddings.vec` (untyped). Per `(model, target_kind)` partial HNSW indexes with an expression cast, e.g. `USING hnsw ((vec::vector(1024)) vector_cosine_ops) WHERE model='qwen3-embedding:0.6b' AND target_kind='chunk_header'`. MRL-capable models also get truncated expression indexes via `l2_normalize(subvector(vec, 1, 256))`, with no re-embedding. Because every indexed vector is in the slice, no SQL post-filter is needed; the shared collapse helper still applies the slice filter before dedupe and cut (D-039). Query rules: the model and kind literals are inlined into the SQL after validation against `embed.KnownModels()` and the kinds enum (or `plan_cache_mode=force_custom_plan` is set), because a generic prepared plan cannot match a partial-index predicate; `hnsw.ef_search` is set to at least the query `LIMIT`, because HNSW returns at most `ef_search` rows. Quality gate: `docs/plan/eval-protocol.md#ann-overlap`.

## Query service

`cmd/server`: router, retrievers, fusion, Q&A loop, trace, trace UI. Endpoints: `POST /search`, `POST /ask`, `GET /trace/{id}`, `GET /healthz` (exists).

## Router

Rule-based first (backticked identifiers, "who calls", "callers of", "where is X defined", ...); Groq LLM only when rules are not confident. Output `{route: lexical|structural|semantic, symbols[], confidence, reason}`. Confidence below the threshold (tuned on DEV) fans out to all retrievers + fusion. LLM router calls use temperature 0. With `ROUTER_LLM=off` (rules-only, config A3n) the LLM is never called: below the threshold the decision is fan-out.

## Lexical retriever

Exact and trigram match on `symbols.name`/`qualified_name`; FTS on `chunks.search_tsv` with the same identifier splitter applied to the query.

## Structural retriever

Resolve the symbol, recursive CTE over `edges` (`source='treesitter'`) for callers/callees up to depth d; rank by hop distance and resolution confidence; flag ambiguous edges. Returns files containing the callers/callees.

## Semantic retriever

Coarse-to-fine: query embedding, then top-k package summaries, file summaries, function summaries plus raw chunks. File score = weighted sum of direct similarity, package similarity and best function scores (three tunable weights, tuned on DEV only). Returns ranked files. The flat variant (A0/A1) ranks files by best chunk only.

## Keyword baseline (B0)

`internal/retrieve/bm25.go`: in-memory BM25 (k1=1.2, b=0.75, fixed) over the in-slice files at the pinned tag, one document per file, tokenized with the identifier splitter on both sides (D-030). No database or model.

## Fusion

Reciprocal rank fusion over file rankings, k=60 fixed (D-009). No LLM reranker (D-008); listed in README "What I'd do differently".

## LLM client

Groq primary, Ollama fallback; token-bucket rate limiter matched to Groq limits; backoff on 429 (honor Retry-After); `llm_cache` keyed by `(prompt_hash, model)` where `prompt_hash` includes `prompt_version`. Router and Q&A calls use temperature 0. The Groq model ID is read from config and recorded in every result JSON (D-007).

## Q&A loop

`/ask` state = question, evidence set, visited symbols, budget (max 3 hops + token cap). Each hop the LLM sees the question, compressed evidence and a CONSTRAINED candidate list taken from graph edges of current evidence, and returns `{action: fetch|answer, symbols[], reason}`. Stops on answer, hop cap, or a hop that adds no new evidence. Hop and answer calls use temperature 0. Final `file:line` citations are validated against the evidence set; invalid ones are removed. The single-shot baseline (top-10 from the A3 pipeline + one answer call) shares the identical answer prompt constraints (citation format, max length).

## Trace

Every request writes `traces.payload`: route + reason, each retriever's results and scores, every hop and its reason, every LLM call with latency/tokens/cache hit.

## Trace UI

Server-rendered Go `html/template` (htmx allowed): query, route, results, hops, citations. This is the demo UI.
