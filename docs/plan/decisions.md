# Decisions (ADR-lite)

Format: ID, date, decision, why, alternatives rejected. PENDING entries are filled by the named task. IDs are never renumbered or reused. Deleted decisions are listed at the end.

## D-001 Stack fixed (2026-10-08)
Go only; Postgres+pgvector (Phase 1); Ollama (Phase 1); Groq at query time only (Phase 1). Why: reproducible locally at $0. Rejected: Python tooling, hosted vector DB, hosted embeddings.

## D-002 Ollama runs natively (2026-10-08)
Why: Docker on macOS cannot use Metal. Rejected: containerized Ollama (CPU only).

## D-003 Keep the early-built prefix registry, normalization and probe (2026-10-08, revised 2026-10-09)
Built during setup, kept but UNVERIFIED until S0-T05/T07. The registry holds exactly two models: `qwen3-embedding:0.6b` and `nomic-embed-text`. Per-model verification record (card URL, exact query and document prefix, native dim, MRL yes/no, max context): PENDING (S0-T05). Rejected: rebuilding from scratch.

## D-004 `chunks.search_tsv` becomes a regular column (2026-10-08)
A generated column cannot split identifiers. New migration 00002; populated from Go in the chunk stage (S3). Rejected: editing 00001; a SQL splitting function.

## D-005 Slice FROZEN at the end of S1; size by machine-time compute budget (2026-10-08, revised 2026-10-10)
Summary compute budget: two overnight runs of machine time (about 20h). Eligible-function budget = min(25k, measured throughput x 20h x 0.8), throughput measured in S1 on 200 post-skip functions (body of at least 3 lines, D-014) plus about 20 file-level prompts, combined across function, file and package levels. If the full slice has more eligible functions than the budget, drop client-go first, then pkg/controller subpackages by size, never pkg/scheduler, until it fits. The slice never grows beyond this budget. Before the freeze, S1-T12 probes the mining yield on the candidate slice (need at least 30 candidates in tier 1 plus tier 2). If the yield is short, in order: (1) change the composition within the same budget (for example keep client-go and drop more pkg/controller subpackages), (2) extend the mining window further back. The KEP cap (5 of 25) stays. Never changes after S1 (it is the eval scope). Size, calculation, yield-probe outcome and the sha256 of the sorted in-slice path list: PENDING (S1-T13); `indexer walk --verify-slice` (`make verify-slice`, run in S8-T05 and S10-T02) compares the hash. The 20h figure is the only hour budget in the plan; measured throughput is reported as a metric.

## D-006 Pinned Kubernetes tag (2026-10-08)
The latest stable `v1.N.0` whose `go` directive is at most the local Go toolchain (local: go1.27.1). Never changed. Tag, go directive and commit sha: PENDING (S1-T01).

## D-007 Groq at query time only; one model per reported number (2026-10-08)
Groq (current 70B-class Llama) for router and Q&A, Ollama fallback; never for offline indexing; temperature 0. Model ID and date are recorded here and in every result JSON. If the model changes before S8, record the new ID and re-run all DEV numbers before the final TEST run. Model ID: PENDING (S6-T01).

## D-008 No LLM reranker (2026-10-08)
Groq quota. Goes in README "What I'd do differently".

## D-009 RRF with k=60, fixed and untuned (2026-10-08)
Why: 15 DEV queries cannot support tuning another parameter.

## D-010 Eval integrity (2026-10-08)
Two query files (`eval/queries.dev.jsonl`, `eval/queries.test.jsonl`); only TEST is hashed; labels never come from the system under test; TEST runs once per config behind `--final`. TEST sha256 is stored in `eval/queries.test.sha256` (machine-readable, read by `eval freeze --verify` and `--final`): PENDING (S4-T14).

## D-011 Ground-truth call graph uses VTA, not CHA (2026-10-08)
CHA over-approximates interface dispatch and would inflate structural labels. Rejected: CHA, RTA.

## D-012 Structural grading with large label sets (2026-10-08)
Structural labels: grade 2 = files with direct callers (or callees); grade 1 = files at hop 2 in the ground-truth graph, at most 15 files in total. If direct files alone exceed 15, keep the 15 with the most call sites as grade 2 and record the omitted count in `_meta`. Per-query recall is reported.

## D-014 Function summaries use raw call names; skip rules (2026-10-08)
The prompt uses raw call-expression names from parse, permanently, so summarization starts right after parse (S1). Benchmark and production share one `prompt_version`. Functions with body under 3 lines are skipped, where body lines = `end_line - start_line` (signature line excluded); types get no summary (doc comments feed file-summary input). `input_hash` = sha256(prompt_version | code | signature | doc | raw_calls). Rejected: resolved callee names (would make the critical path wait for S2).

## D-015 Token estimate and truncation guard (2026-10-08)
`bytes/4` estimate (documented, golden-file tested). Explicit `num_ctx` on every Ollama request. Two layers ([architecture.md#ollama-requests](architecture.md#ollama-requests)): pre-send, estimated tokens > 0.8 x `num_ctx` means the request is not sent and the row fails with reason `too_large` (summaries: `status='failed'`; embeds: an `embed_failures` row); post-send, embed uses `truncate:false` (error = `truncated`) and generation with `prompt_eval_count >= num_ctx - num_predict` fails as `truncated`. `prompt_eval_count` is not compared with the estimate because the prompt cache can lower it; S0-T07 tests this empirically. Rejected: tokenizer dependency; estimate comparison.

## D-017 AGENTS.md links are plain, not auto-loaded imports (2026-10-08)
`AGENTS.md` mentions `docs/agents/*` as backticked paths, not `@`-imports, so they are read on demand. S0-T09 re-checks that no `@` import exists, re-points `docs/agents/domain.md` to `docs/plan/glossary.md` and `docs/plan/decisions.md`, and fixes the stale "Domain docs" line in `AGENTS.md`.

## D-018 Q&A comparison (2026-10-08)
`/ask` multi-hop versus single-shot RAG (top-10 from A3 + one call); identical answer prompt constraints and rendering; grading sheet shows only answer and citations, seeded A/B shuffle; tokens and LLM calls reported for both. LLM-only baseline rejected.

## D-019 S2 re-parses; raw_calls is for summaries only (2026-10-08)
Call resolution needs local variable types and import aliases that `raw_calls` lacks.

## D-020 Tuning scope (2026-10-08, revised 2026-10-09)
Tuned on DEV only, coarse grids (at most 12 combinations per parameter set), every result logged: the three semantic file-score weights (A2), the router threshold for A3 and, separately, for A3n. Values recorded here BEFORE the single final TEST run in S8. Values: PENDING (S5-T08, S6-T12).

## D-022 Bootstrap (2026-10-08)
Paired, 10,000 seeded resamples over queries, 95% percentile CIs.

## D-023 Ambiguous edges (2026-10-08)
One `edges` row per candidate callee, `resolution='ambiguous'`.

## D-024 Semantic query sourcing (2026-10-08)
PRs merged within 12 months before the tag, labeled on the tag's file state. Rules, fallbacks and the leakage filter: [eval-protocol.md#sources](eval-protocol.md#sources). Limitation: the fix is already in the tag.

## D-025 callgraph-gt invocation (2026-10-08)
`go list -deps ./pkg/scheduler/...` must succeed inside the clone with the network disabled. Working invocation: PENDING (S2-T06).

## D-027 ANN overlap probe set before DEV exists (2026-10-08)
S3 precedes S4, so S3 measures ANN overlap@10 on 30 seeded queries made from doc-comment first sentences; re-run on DEV in S4 and S8; below 95% means tune `ef_search` first.

## D-028 Final tuned values (2026-10-08)
PENDING (S8-T04): weights, thresholds, `ef_search` per model, dimensions.

## D-029 Embeddings cover the slice only (2026-10-09)
Vectors (raw chunks for A0, header chunks, summaries) exist for the slice only. Chunks, `search_tsv` and edges cover the whole filtered repo (cheap, no GPU). Why: simpler, smaller indexes, no iterative-scan or post-filter recipe; whole-repo breadth is demonstrated by Phase 2. Rejected: whole-repo embeddings.

## D-030 B0 keyword baseline is BM25 (2026-10-09)
Definition: [architecture.md#keyword-baseline-b0](architecture.md#keyword-baseline-b0) (BM25, fixed k1 and b, identifier splitter, in-slice files). Rejected: raw match counts (they favor large files).

## D-031 A3n and the E/M configurations (2026-10-09)
Definition of A3n, E1, E2 and M1: [eval-protocol.md#configurations](eval-protocol.md#configurations). Why: A3n is the Phase 1 reference matching the Phase 2 default mode (flat header retriever, rules-only router, no summaries); E1, E2 and M1 run under A3n.

## D-032 Specs and tickets live in `.scratch/` (2026-10-09)
`.scratch/<sprint>/spec.md` and `.scratch/<sprint>/issues/NN-slug.md` (the skills' default, already described in `docs/agents/issue-tracker.md`), so no auto-loaded file changes. Sprint directory names: `S<n>-<slug>`, `P2-S<n>-<slug>`. Rejected: `docs/plan/tickets/`.

## D-033 MCP server is Phase 2 only (2026-10-09)
It needs the Store refactor (P2-S1); a Phase 1 version would be thrown away.

## D-034 Phase 2 tool name is `lodestone` (2026-10-09)
Binary `cmd/lodestone`, index dir `.lodestone/`. Checked: no Homebrew formula with that name. GitHub, `go install` and `brew search` collisions are checked in P2-S9-T09 before the first release and the outcome is written here. Renaming later only touches strings.

## D-035 Windows is out of scope (2026-10-09)
No test machine; cgo/runtime/install complexity. Code stays OS-clean (`os.UserCacheDir`, no hard-coded paths).

## D-036 S9 rule: heavy-mode default model (2026-10-09)
Default heavy-mode model = the 3B coder model unless the paired-bootstrap 95% CI of (7B minus 3B) Recall@10 on TEST excludes 0 in favor of 7B. Note `eval compare --base A2 --cand A2-3B` prints 3B minus 7B, so the decision function negates it. Peak memory is reported next to it. A2-3B reuses the A2 weights (no retuning). This decision uses TEST on purpose (it is a product default, not a tuning step); the Phase 2 model and dimension choice (D-041) does not. Decision: PENDING (S9-T08).

## D-037 TEST rule for new configurations (2026-10-09)
Each new configuration (A3n, B0, A2-3B, P2-float, P2-compact) gets exactly one TEST run, after its own DEV tuning, in its own `eval/results/<config>/`. No Phase 1 config is ever re-run on TEST. The TEST hash is verified (`eval freeze --verify`) before every `--final` run.

## D-038 Memory measurement method (2026-10-09)
Method and its limits: [eval-protocol.md#memory-measurement](eval-protocol.md#memory-measurement). README claims name the measurement behind them. "Usable on an 8GB machine" is an inference, not a test on 8GB hardware.

## D-039 Slice filter runs before dedupe and the top-10 cut, in both phases (2026-10-09)
Implemented once in the shared `internal/retrieve` collapse helper; a test proves the order (S3-T10, P2-S8-T01). In Phase 2 the slice prefixes are additionally applied inside the vector search (`VectorQuery.PathPrefixes`), because the Phase 2 index holds whole-repo vectors and a post-hoc filter after top-k would change the candidate set.

## D-040 How CI protects the Phase 1 path (2026-10-09)
Three checks on every push after P2-S1-T02: Postgres integration tests with the service container; a fixture end-to-end (toy repo, labeled fixture queries, fake deterministic embedder) with golden JSON for A1, A3n and B0; `eval freeze --verify`. A task closes only with CI green; any task touching `internal/` lists `go test ./internal/e2e/...` in its acceptance.

## D-041 Phase 2 model, dimension and int8 selection rule (2026-10-09)
Inputs are Phase 1 DEV results only; Phase 1 TEST results for E1/E2/M1 are reported but never used for the choice. (1) Model: compare E1 and E2 under A3n on DEV; higher DEV nDCG@10 wins; if the paired-bootstrap CI of the difference includes 0, pick the lower-memory model. (2) Dimension: from M1 DEV under the same config, 256 unless DEV shows an nDCG@10 loss whose CI excludes 0. (3) int8: Phase 1 never tests int8; it is decided by the Phase 2 compactness ablation on DEV (native float versus D-056 dimension int8) before that configuration's single TEST run, same rule: int8 unless DEV shows a loss whose CI excludes 0 (outcome in D-057). Fallback if E2 or M1 DEV results are missing because they were dropped: qwen3 at its native dimension. README caveat: with 15 DEV queries the CIs are wide, so the choice is largely driven by the memory tie-breaker and must not be described as a clear quality win.

## D-042 Agent-efficiency experiment controls (2026-10-09)
Controls, metrics and limitations: [eval-protocol.md#phase-2-agent-efficiency](eval-protocol.md#phase-2-agent-efficiency). Exploratory, capped at 20 runs, lowest priority, grading not blind (stated limitation).

## D-043 Multi-language smoke test (2026-10-09)
Smoke test definition: [eval-protocol.md#phase-2-smoke-test](eval-protocol.md#phase-2-smoke-test). One public repo per tier-1 language, each pinned by commit sha, queries committed before the first lodestone run. Repos and shas: PENDING (P2-S8-T07).

## D-044 The repository stays public (2026-10-09)
Free hosted macOS arm64 and Linux arm64 CI runners require a public repository; release builds depend on them.

## D-045 Planning rules (2026-10-09)
No time or hour estimates anywhere in plans, specs or tickets (no capacity, velocity, weekend mapping). Each task is one ticket, implemented in one fresh agent session, producing one reviewable diff; anything larger is split.

## D-046 Phase 1 approval gate (2026-10-09)
Phase 2 starts only after the author explicitly approves Phase 1. Approval (date and the author's words): PENDING (S10-T03).

## D-047 Phase 2 approval gate (2026-10-09)
Phase 2 is done only after the author tests and approves it. Approval: PENDING (P2-S10-T03).

## D-048 Native CI runners, no cgo cross-compiling (2026-10-09)
Build and test on macos-14 (arm64), ubuntu amd64 and ubuntu arm64 runners.

## D-049 Phase 2 default index includes test files (2026-10-09)
Test files are indexed and flagged `is_test`; users control scope with ignore globs in `.lodestone/config.toml`. The Kubernetes parity run uses an eval config that replicates the Phase 1 exclusions, with the slice applied at query time through the shared helper.

## D-050 Phase 2 scope limits (2026-10-09)
No Docker, Ollama, Python, API keys or sign-up needed for default mode; no Groq; no tiny bundled chat model; heavy mode is opt-in. Phase 1 stays working (D-040).

## D-051 This plan supersedes the earlier plan (2026-10-09)
The plan was rewritten from the new brief; deletions are listed below.

## D-052 SQLite driver and vector approach (PENDING P2-S0-T02)
## D-053 Inference runtime: in-process cgo versus managed subprocess (PENDING P2-S0-T04)
## D-054 Go MCP SDK (PENDING P2-S0-T05)
## D-055 GGUF sources, quantization, probe results, reference-cosine threshold (PENDING P2-S0-T03 for sources and P2-S0-T06 for probe results)
## D-056 Phase 2 embedding model and dimension, applying D-041 (PENDING P2-S0-T01)
## D-057 int8 decision from the compactness ablation on DEV (PENDING P2-S8-T02)

## D-058 One build context for walk and callgraph-gt (2026-10-10)
`GOOS=linux`, `GOARCH=amd64`, `CgoEnabled=false` in both the S1 walk filter and the S2 `callgraph-gt` load (plus `GOWORK=off`, `GOTOOLCHAIN=local`, `GOFLAGS=-mod=vendor` for `callgraph-gt`). Why: tree-sitter indexes every file it is given, `go/packages` loads only one build context; unequal file sets make call-graph precision/recall and structural labels incomparable. Acceptance: the slice `.go` file set from `walk` equals the set loaded by `go/packages` (S2-T06). Rejected: host build context (darwin).

## D-059 Ignore-file library (PENDING P2-S3-T02)

## D-061 S0 process: one branch, one commit per ticket, draft PR for CI (2026-10-10)
Work happens on branch `s0-setup-verification`. One commit per ticket. Agents push only when CI proof is needed and after showing the owner the workflow triggers and GitHub auth status; a draft pull request triggers CI. The owner reviews and merges; agents never push to or merge into `main`.

## Deleted decisions
- D-013 (ANN recipe with iterative scan, exact re-sort and slice post-filter): superseded by D-029 (slice-only embeddings, no post-filter in SQL). The ANN overlap gate itself is kept in D-027.
- D-016 (embedding variants with whole-repo header vectors): superseded by D-029.
- D-021 (cut order in Phase 1): replaced by the Phase 1 priority list in ROADMAP.md.
- D-026 (capacity assumption): deleted with all hour estimates; also removed: weekend mapping, capacity discussion, velocity rules.
- The third embedding candidate (provisional) and its comparison configuration E3, both from the earlier plan: removed from scope; the registry keeps two models (D-003).
