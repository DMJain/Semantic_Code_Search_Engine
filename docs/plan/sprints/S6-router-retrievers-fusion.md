# S6 - Router, retrievers, fusion, LLM client

## Goal
Build the Groq/Ollama LLM client with rate limiting and cache, the lexical and structural retrievers, the rule+LLM router, RRF fusion, traces and `/search`; evaluate A3, A3n and A4 on DEV with router and system metrics. A3n (rules-only router, no summaries, no LLM) is the reference for Phase 2 parity (D-031).

## Entry criteria
S5 done (A2 works, weights recorded). `GROQ_API_KEY` set in `.env`. Edges (treesitter) populated.

## Links
[architecture.md#llm-client](../architecture.md#llm-client), [#router](../architecture.md#router), [#lexical-retriever](../architecture.md#lexical-retriever), [#structural-retriever](../architecture.md#structural-retriever), [#fusion](../architecture.md#fusion), [#trace](../architecture.md#trace), [#query-service](../architecture.md#query-service), [#result-contract](../architecture.md#result-contract); [eval-protocol.md#router-metrics](../eval-protocol.md#router-metrics), [#system-metrics](../eval-protocol.md#system-metrics), [#reproducibility](../eval-protocol.md#reproducibility), [#configurations](../eval-protocol.md#configurations). Decisions: D-007, D-008, D-009, D-020, D-031, D-039.

## Task order
Client and cache first (T01-T03), retrievers (T04-T05), router (T06-T07), fusion and traces (T08-T09), server and evaluation (T10-T12).

## Tasks

### S6-T01 - Groq client
- Why: Q&A and router LLM calls (D-007).
- Depends on: none
- Touches: `internal/llm/groq.go` (replace the stub), `internal/config/config.go`, `.env.example`, `docs/plan/decisions.md` (D-007)
- Details: OpenAI-compatible `POST https://api.groq.com/openai/v1/chat/completions`, bearer `GROQ_API_KEY`, body from `CompletionRequest` (system + messages, temperature, `max_tokens`), parse `choices[0].message.content` and `usage`. Choose the current 70B-class model by reading Groq's models page at implementation time; set `GROQ_MODEL` default; record ID, date and free-tier RPM/TPM/RPD in D-007. Error mapping: 429 -> typed `RateLimitError{RetryAfter}`; 5xx -> retryable; 4xx -> permanent. Unset key -> `ErrNotConfigured` with the message `set GROQ_API_KEY`. `CompletionResponse` gains `Latency` and `CacheHit bool`.
- Acceptance: `go test -count=1 ./internal/llm/...` passes; guarded live test (`GROQ_API_KEY` set) returns non-empty text for "Say OK"; D-007 contains model ID, date, limits.
- Tests: `httptest.Server` for success, 429 with `Retry-After`, 500, 401, malformed JSON.
- Out of scope: rate limiting (T02), cache (T03).
- Owner: agent

### S6-T02 - Token-bucket limiter and 429 backoff
- Why: stay inside Groq free-tier limits.
- Depends on: T01
- Touches: `internal/llm/ratelimit.go`
- Details: decorator `RateLimited(inner LLM, cfg{RPM, TPM})`: two token buckets (requests and estimated tokens: prompt estimate by [#token-estimate](../architecture.md#token-estimate) plus `max_tokens`); `Complete` waits (context-aware) for both; on `RateLimitError` sleep `max(RetryAfter, backoff)` with exponential backoff 1s, 2s, 4s (max 3 retries) with jitter, then return the error. Limits from env `GROQ_RPM`, `GROQ_TPM` defaults from D-007. Clock injectable for tests.
- Acceptance: unit test with a fake clock: with RPM=2, three calls issued at t=0 complete at t=0, 0, 30s; a 429 with Retry-After 5 delays the retry by >= 5s; context cancel returns promptly.
- Tests: fake-clock tests; concurrency test with `-race`.
- Out of scope: cache, fallback.
- Owner: agent

### S6-T03 - llm_cache wrapper, Ollama fallback, cache export
- Why: reproducibility and cost ([eval-protocol.md#reproducibility](../eval-protocol.md#reproducibility)).
- Depends on: T02
- Touches: `internal/llm/cache.go`, `internal/llm/fallback.go`, `cmd/eval/cacheexport.go`
- Details: `Cached(inner, store, promptVersion)`: key `prompt_hash = sha256(promptVersion | system | messages | temperature | max_tokens)`, `model` = the configured model name of `inner` (known before the call); hit returns stored response with `CacheHit=true` and zero latency; miss calls inner and stores. `WithFallback(primary, ollama)`: on `RateLimitError` after retries, 5xx, or timeout use the Ollama LLM and mark `Model` as the Ollama model (so cache entries and result JSON show which model answered). `eval cache-export --out eval/results/llm_cache.jsonl.gz` dumps `llm_cache` gzipped JSONL. Prompt version is a required argument; no default.
- Acceptance: second identical call is a hit (unit test counts inner calls = 1); changing `promptVersion` misses; fallback test returns the Ollama response and model name when the primary returns 429 x4; `eval cache-export` writes a gz file whose line count equals `select count(*) from llm_cache`.
- Tests: unit tests with in-memory store fake, httptest for fallback; guarded integration test with Postgres.
- Out of scope: wiring into server (T10).
- Owner: agent

### S6-T04 - Lexical retriever
- Why: exact/identifier queries ([#lexical-retriever](../architecture.md#lexical-retriever)).
- Depends on: none
- Touches: `internal/retrieve/lexical.go`
- Details: for query `q`: (1) exact match on `symbols.name` and `qualified_name` (case-sensitive) score 1.0; (2) trigram `similarity(name, q)` over `symbols.name`/`qualified_name` with `%` operator, threshold 0.3, score = similarity * 0.8; (3) FTS: `chunks.search_tsv @@ websearch_to_tsquery('simple', <SplitIdentifiers(q) joined>)` ordered by `ts_rank`, score normalized by max in result set * 0.6. Restrict to in-slice for evaluation (config flag `SliceOnly`, default true in eval; the server flag is on). Hits collapse to files via the shared helper; `Evidence` carries symbol/chunk and line range.
- Acceptance: with the real index, `where is schedulePod defined` returns the defining file first; a fixture test shows `pod backoff` returns the fixture file containing `podBackoff` in the top 5, and on the real index the top 5 contains at least one file whose `search_tsv` matches both `pod` and `backoff` (SQL check recorded in Results); unit tests pass.
- Tests: guarded integration test with 20-symbol fixture (exact, trigram typo `schedulPod`, split fragment); unit test for score blending.
- Out of scope: ranking tuning.
- Owner: agent

### S6-T05 - Structural retriever
- Why: caller/callee questions ([#structural-retriever](../architecture.md#structural-retriever)).
- Depends on: none
- Touches: `internal/retrieve/structural.go`
- Details: input from the router (`symbols[]`, direction callers|callees inferred from phrasing "who calls"/"callers of" vs "what does X call"/"callees of"; default callers). Resolve symbol: exact `qualified_name`, else exact `name`; multiple matches -> take all (flag `ambiguous_symbol`). Recursive CTE over `edges` where `source='treesitter'` up to depth `d` (default 2, max 3) with cycle guard (`path` array); each reached symbol gets score `conf / hop` where `conf` = 1.0 exact, 0.7 name_only, 0.4 ambiguous; best score per symbol; collapse to files via shared helper; `Evidence` marks `ambiguous` and hop distance. Excludes the query symbol's own file only if no other file results.
- Acceptance: for a symbol with known callers (take one from the S4 DEV structural query), the retriever returns the files of its direct callers ahead of 2-hop files; cycle test terminates; unit tests pass.
- Tests: guarded integration test with a fixture graph (cycle, ambiguous edge, 3 hops); unit test for scoring.
- Out of scope: using `gopackages` edges (forbidden at serving time).
- Owner: agent

### S6-T06 - Rule-based router
- Why: cheap, deterministic routing first ([#router](../architecture.md#router)).
- Depends on: none
- Touches: `internal/retrieve/router_rules.go`, `internal/retrieve/retrieve.go` (`RouteDecision`)
- Details: `RouteDecision` becomes `{Route, Retrievers[], Symbols[], Confidence float64, Reason string, ViaLLM bool}`. Rules, in order: backticked identifier(s) -> symbols; `who calls|callers of|what calls|callees of|what does <X> call` -> structural conf 0.9; `where is <X> defined|find <X>|definition of` or the whole query being a single identifier-looking token -> lexical conf 0.9; query containing a backticked identifier but no structural phrase -> lexical conf 0.7; natural-language question with >= 6 words and no identifiers -> semantic conf 0.7; else conf 0.4. Table of rules lives in one slice of `Rule{Name, Match, Route, Conf}`; `Reason` names the rule.
- Acceptance: golden table test with >= 30 query strings, none copied from DEV (DEV strings are added only after S6-T12 as regression rows), passes; decision for "who calls `schedulePod`" = structural, conf >= 0.9, symbols=[schedulePod].
- Tests: golden table.
- Out of scope: LLM router (T07), threshold tuning (T12).
- Owner: agent

### S6-T07 - LLM router fallback and fan-out
- Why: handle low-confidence queries.
- Depends on: T03, T06
- Touches: `internal/retrieve/router_llm.go`, `internal/retrieve/router.go`
- Details: `prompt_version = "router-v1"`; system prompt: define the three routes with one example each; ask for JSON only `{"route":"...","symbols":["..."],"confidence":0.0-1.0,"reason":"..."}`; temperature 0, `max_tokens` 150. Parse strictly; one repair retry on invalid JSON, then fall back to fan-out. Composite `Router`: rules -> if `Confidence < threshold` (config `ROUTER_THRESHOLD`, default 0.5; rule confidences are 0.4, 0.7 and 0.9) call the LLM router; if the LLM's confidence is also < threshold, `Retrievers = {lexical, structural, semantic}` (fan-out) with `Reason` saying so. LLM failure (any error after fallback) -> fan-out. Config `ROUTER_LLM=off` selects rules-only mode (used by A3n): below the threshold the decision goes straight to fan-out and the LLM is never constructed or called.
- Acceptance: unit tests with a fake LLM: high-confidence rule never calls the LLM; low-confidence calls it once (cache hit on second identical query); invalid JSON twice -> fan-out; with `ROUTER_LLM=off` a low-confidence query fans out and the fake LLM records 0 calls; guarded live test routes "how does the kubelet decide to evict pods under memory pressure" to semantic.
- Tests: fake-LLM unit tests for each branch.
- Out of scope: threshold tuning (T12).
- Owner: agent

### S6-T08 - RRF fusion and pipelines
- Why: A3, A3n and A4 ([#fusion](../architecture.md#fusion)).
- Depends on: T04, T05, T07
- Touches: `internal/retrieve/fusion.go`, `internal/retrieve/pipeline.go`, `internal/evalconfig/`
- Details: `RRF(lists [][]Result, k=60, limit) []Result`: score = sum over lists of 1/(k+rank) (rank from 1), sums evidence, ties by best original score then path. `Pipeline{Router, Lexical, Structural, Semantic(A2 weights from D-020)}`: A3 = run the retrievers named by the router decision (one retriever -> its list as is; several -> RRF). A4 = always run all three and RRF. A3n = the A3 pipeline with `ROUTER_LLM=off` and the flat header retriever (`Flat{header}`, the A1 retriever) in place of the coarse-to-fine retriever, so it needs no summaries and makes no LLM call (D-031). Register `A3`, `A3n`, `A4` in `evalconfig`.
- Acceptance: unit test: two lists with a shared file ranks the shared file first, scores equal the hand computation with k=60; A3 with a structural decision calls only the structural retriever (spy test); A4 always calls three; A3n never calls a summary retriever and never calls the LLM (spy tests).
- Tests: RRF hand-computed tests; spy-retriever tests.
- Out of scope: tuning k (D-009: fixed).
- Owner: agent

### S6-T09 - Trace recorder
- Why: every request must be inspectable ([#trace](../architecture.md#trace)).
- Depends on: T03, T08
- Touches: `internal/trace/`, `internal/llm/` (instrumentation)
- Details: `trace.Recorder` carried in `context.Context`; events: `route{decision}`, `retriever{kind,query,results[file,score,evidence],latency_ms}`, `fusion{inputs,outputs}`, `llm{prompt_version,model,latency_ms,prompt_tokens,completion_tokens,cache_hit,fallback}`, `hop{n,action,symbols,reason}` (used in S7), `error{...}`. `Recorder.Save(ctx, store, query) (id)` inserts into `traces` (`route` column + `payload` jsonb with a `schema_version: 1`). The `LLM` decorators append `llm` events automatically from context.
- Acceptance: unit test: a fake pipeline produces a payload with route, per-retriever results and one llm event; guarded integration test saves and reloads it by ID byte-equal; payload `schema_version` = 1.
- Tests: unit and guarded integration tests.
- Out of scope: UI (S7).
- Owner: agent

### S6-T10 - Server: `/search` and `/trace/{id}` (JSON)
- Why: query endpoints ([#query-service](../architecture.md#query-service)).
- Depends on: T09
- Touches: `cmd/server/`
- Details: `POST /search {"query": "...", "config": "A3"(default), "limit": 10}` -> `{"trace_id", "route", "results":[{file,score,evidence}]}`; wires the stack: Ollama embedder, the LLM stack `WithFallback(Cached(RateLimited(Groq), GROQ_MODEL), Cached(Ollama, OLLAMA model))`, retrievers, router, recorder. `GET /trace/{id}` returns the raw JSON payload (HTML view is S7). Validation: empty query -> 400; unknown config -> 400. `/healthz` unchanged but adds Groq as an optional (non-fatal) check.
- Acceptance: with the server running, `curl -s -XPOST localhost:8080/search -d '{"query":"who calls `schedulePod`"}'` returns route `structural`, <= 10 results and a `trace_id`; `curl localhost:8080/trace/<id>` returns the payload; 400 cases tested; `/healthz` still 200 with Groq absent.
- Tests: handler tests with fakes for every dependency.
- Out of scope: `/ask`, HTML.
- Owner: agent

### S6-T11 - Harness: A3/A4, router metrics, system metrics
- Why: [#router-metrics](../eval-protocol.md#router-metrics) and [#system-metrics](../eval-protocol.md#system-metrics).
- Depends on: T10, S4-T02
- Touches: `cmd/eval/`
- Details: `eval run` for A3/A4 records, per query: route decision, `via_llm`, latency, LLM calls, tokens, cache hits (read from the trace). Aggregates: router accuracy vs labeled route (A3 only), confusion matrix (3x3 + `fanout` column), percent rule-routed vs LLM-routed vs fan-out; latency p50/p95 per route; LLM calls and tokens per query; cache hit rate. Result JSON records the Groq model ID. Report sections added to `eval report`.
- Acceptance: `eval run --config A3 --split dev` writes JSON with `router.accuracy`, `router.confusion`, `router.share_rule/llm/fanout`, `system.latency_ms.{p50,p95}` per route, `system.llm_calls_per_query`, `system.cache_hit_rate`; unit tests with synthetic traces pass.
- Tests: metric unit tests (percentile on known data, confusion on known decisions).
- Out of scope: tuning.
- Owner: agent

### S6-T12 - Tune thresholds on DEV; run A3, A3n and A4
- Why: D-020 limits tuning to DEV, coarse grid.
- Depends on: T11
- Touches: `eval/results/{A3,A3n,A4}/dev.json`, `eval/results/tuning/s6-threshold.json`, `docs/plan/decisions.md` (D-020)
- Details: grid for `ROUTER_THRESHOLD` in {0.3, 0.5, 0.8, 0.95} (each gives a different behavior for the rule confidences 0.4, 0.7, 0.9: none sent to the LLM, only the 0.4 rules, the 0.4 and 0.7 rules, all), run separately for A3 and for A3n (two parameter sets, each <= 12 combinations), each row logged; choose per config by DEV overall nDCG@10 (ties: higher rule-routed share). Record both thresholds in D-020. Run A3, A3n and A4 on DEV, `eval compare` against A0 and A2, `eval report`. Re-check ANN overlap is still >= 95% (unchanged index; one command).
- Acceptance: tuning JSON rows <= 12; D-020 has the threshold; `eval/results/A3/dev.json`, `A3n/dev.json`, `A4/dev.json` exist; report shows A0..A4, A3n and B0 with CIs; Groq model ID recorded in the JSON.
- Tests: none.
- Out of scope: TEST run; any other tuning.
- Owner: agent

## Measurements to record
- A3, A3n, A4 DEV retrieval metrics with CIs vs A0 and A2: `eval/results/{A3,A3n,A4}/dev.json`, `eval/report.md`.
- Router accuracy, confusion matrix, rule/LLM/fan-out shares ([#router-metrics](../eval-protocol.md#router-metrics)).
- Latency p50/p95 per route, LLM calls/tokens per query, cache hit rate ([#system-metrics](../eval-protocol.md#system-metrics)).
- Groq model ID, free-tier limits (D-007); tuning grid JSON.

## Exit criteria / Definition of Done
`/search` and `/trace/{id}` work end to end; all LLM calls go through the stack fallback(cache(limiter(Groq))); A3/A3n/A4 DEV recorded; D-007 and D-020 filled; ROADMAP updated.

## Risks + mitigations
- Groq rate limits during DEV runs: cache makes reruns free; fallback to Ollama marks the model in results (a run containing fallback answers is flagged and rerun).
- Rule router overfits to DEV phrasing: rules are written from the brief's patterns, not from DEV text; the 30-query golden table includes strings not in DEV.
- Structural retriever precision depends on edge quality: evidence flags ambiguous edges; result discussed with S2 numbers.

## Demo check
`curl` a structural, lexical and semantic query to `/search`; open each `/trace/{id}` JSON; `eval compare --base A0 --cand A3 --split dev` prints the table.

## Results
(empty)
