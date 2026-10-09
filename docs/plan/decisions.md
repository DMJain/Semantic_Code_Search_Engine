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

## D-005 Slice FROZEN at the end of S1; size by machine-time compute budget (2026-10-08, revised 2026-10-09)
Summary compute budget: two overnight runs of machine time (about 20h). Slice size = min(25k functions, measured throughput x 20h x 0.8), throughput measured in S1 on 200 post-skip functions (body of at least 3 lines) plus about 20 file-level prompts, combined across function, file and package levels. If under 15k, drop client-go first, then pkg/controller subpackages by size; never pkg/scheduler. Never changes after S1 (it is the eval scope). Size, calculation and the sha256 of the sorted in-slice path list: PENDING (S1-T11). This machine-time budget is the only hour figure in the plan.

## D-006 Pinned Kubernetes tag (2026-10-08)
The latest stable `v1.N.0` whose `go` directive is at most the local Go toolchain (local: go1.27.1). Never changed. Tag, go directive and commit sha: PENDING (S1-T01).

## D-007 Groq at query time only; one model per reported number (2026-10-08)
Groq (current 70B-class Llama) for router and Q&A, Ollama fallback; never for offline indexing; temperature 0. Model ID and date are recorded here and in every result JSON. If the model changes before S8, record the new ID and re-run all DEV numbers before the final TEST run. Model ID: PENDING (S6-T01).

## D-008 No LLM reranker (2026-10-08)
Groq quota. Goes in README "What I'd do differently".

## D-009 RRF with k=60, fixed and untuned (2026-10-08)
Why: 15 DEV queries cannot support tuning another parameter.

## D-010 Eval integrity (2026-10-08)
Two query files (`eval/queries.dev.jsonl`, `eval/queries.test.jsonl`); only TEST is hashed; labels never come from the system under test; TEST runs once per config behind `--final`. TEST sha256: PENDING (S4-T14).

## D-011 Ground-truth call graph uses VTA, not CHA (2026-10-08)
CHA over-approximates interface dispatch and would inflate structural labels. Rejected: CHA, RTA.

## D-012 Structural grading with large label sets (2026-10-08)
Over 15 files: direct callers primary (2), rest secondary (1); per-query recall reported.

## D-014 Function summaries use raw call names; skip rules (2026-10-08)
The prompt uses raw call-expression names from parse, permanently, so summarization starts right after parse (S1). Benchmark and production share one `prompt_version`. Functions with body under 3 lines are skipped; types get no summary (doc comments feed file-summary input). `input_hash` = code + signature + doc + raw_calls. Rejected: resolved callee names (would make the critical path wait for S2).

## D-015 Token estimate and truncation guard (2026-10-08)
bytes/4 estimate (documented, golden-file tested); explicit `num_ctx` on every Ollama request; `prompt_eval_count` mismatch fails the row as `truncated`. Rejected: tokenizer dependency.

## D-017 AGENTS.md links are plain, not auto-loaded imports (2026-10-08)
`AGENTS.md` mentions `docs/agents/*` as backticked paths, not `@`-imports, so they are read on demand. S0-T09 re-checks that no `@` import exists and re-points `docs/agents/domain.md` to `docs/plan/glossary.md` and `docs/plan/decisions.md`.

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
PRs merged within 12 months before the tag, labeled on the tag's file state; fallbacks in order: widen the window by 6 months, raise the cap to 8 files (manual per-file review, incidental changes graded 1 with reason), then KEP/doc questions (max 5 of 25). Limitation: the fix is already in the tag.

## D-025 callgraph-gt invocation (2026-10-08)
`go list -deps ./pkg/scheduler/...` must succeed inside the clone with the network disabled. Working invocation: PENDING (S2-T05).

## D-027 ANN overlap probe set before DEV exists (2026-10-08)
S3 precedes S4, so S3 measures ANN overlap@10 on 30 seeded queries made from doc-comment first sentences; re-run on DEV in S4 and S8; below 95% means tune `ef_search` first.

## D-028 Final tuned values (2026-10-08)
PENDING (S8-T04): weights, thresholds, `ef_search` per model, dimensions.

## D-029 Embeddings cover the slice only (2026-10-09)
Vectors (raw chunks for A0, header chunks, summaries) exist for the slice only. Chunks, `search_tsv` and edges cover the whole filtered repo (cheap, no GPU). Why: simpler, smaller indexes, no iterative-scan or post-filter recipe; whole-repo breadth is demonstrated by Phase 2. Rejected: whole-repo embeddings.

## D-030 B0 keyword baseline is BM25 (2026-10-09)
In-memory term index over the in-slice files at the pinned tag, one document per file, identifier splitter on documents and query, standard BM25 with fixed k1=1.2 and b=0.75 (not tuned). Rejected: raw match counts (they favor large files).

## D-031 A3n and the E/M configurations (2026-10-09)
A3n = A3 with the flat header retriever instead of coarse-to-fine, rules-only router (no LLM; fan-out + RRF below the threshold), no summaries. It is the Phase 1 reference matching Phase 2 default mode. E1 (qwen3) and E2 (nomic) and M1 (native vs 256d) run under A3n.

## D-032 Specs and tickets live in `.scratch/` (2026-10-09)
`.scratch/<sprint>/spec.md` and `.scratch/<sprint>/issues/NN-slug.md` (the skills' default, already described in `docs/agents/issue-tracker.md`), so no auto-loaded file changes. Sprint directory names: `S<n>-<slug>`, `P2-S<n>-<slug>`. Rejected: `docs/plan/tickets/`.

## D-033 MCP server is Phase 2 only (2026-10-09)
It needs the Store refactor (P2-S1); a Phase 1 version would be thrown away.

## D-034 Phase 2 tool name is `lodestone` (2026-10-09)
Binary `cmd/lodestone`, index dir `.lodestone/`. Checked: no Homebrew formula with that name; GitHub and `go install` path collisions to be checked before the first release (P2-S9-T09). Renaming later only touches strings.

## D-035 Windows is out of scope (2026-10-09)
No test machine; cgo/runtime/install complexity. Code stays OS-clean (`os.UserCacheDir`, no hard-coded paths).

## D-036 S9 rule: heavy-mode default model (2026-10-09)
Default heavy-mode model = the 3B coder model unless the paired-bootstrap 95% CI of (7B minus 3B) Recall@10 on TEST excludes 0 in favor of 7B. Peak memory is reported next to it. A2-3B reuses the A2 weights (no retuning). Decision: PENDING (S9-T07).

## D-037 TEST rule for new configurations (2026-10-09)
Each new configuration (A3n, B0, A2-3B, P2-float, P2-compact) gets exactly one TEST run, after its own DEV tuning, in its own `eval/results/<config>/`. No Phase 1 config is ever re-run on TEST. The TEST hash is verified (`eval freeze --verify`) before every `--final` run.

## D-038 Memory measurement method (2026-10-09)
macOS: physical footprint (`footprint` tool or `phys_footprint`), because RSS undercounts Metal/unified-memory buffers and mmapped weights. Linux CI smoke job: container cgroup `memory.peak`. Both numbers are reported and the README says which measurement backs each claim. "Usable on an 8GB machine" is an inference from measured footprints plus a Linux job under `--memory=2g`, not a test on 8GB hardware.

## D-039 Slice filter runs before dedupe and the top-10 cut, in both phases (2026-10-09)
Implemented once in the shared `internal/retrieve` collapse helper; a test proves the order (S3-T10, P2-S8-T01).

## D-040 How CI protects the Phase 1 path (2026-10-09)
Three checks on every push after P2-S1-T01: Postgres integration tests with the service container; a fixture end-to-end (toy repo, labeled fixture queries, fake deterministic embedder) with golden JSON for A1, A3n and B0; `eval freeze --verify`.

## D-041 Phase 2 model, dimension and int8 selection rule (2026-10-09)
Inputs are Phase 1 DEV results only; Phase 1 TEST results for E1/E2/M1 are reported but never used for the choice. (1) Model: compare E1 and E2 under A3n on DEV; higher DEV nDCG@10 wins; if the paired-bootstrap CI of the difference includes 0, pick the lower-memory model. (2) Dimension: from M1 DEV under the same config, 256 unless DEV shows an nDCG@10 loss whose CI excludes 0. (3) int8: Phase 1 never tests int8; it is decided by the Phase 2 compactness ablation on DEV (native float versus D-056 dimension int8) before that configuration's single TEST run, same rule: int8 unless DEV shows a loss whose CI excludes 0 (outcome in D-057). Fallback if E2 or M1 DEV results are missing because they were dropped: qwen3 at its native dimension. README caveat: with 15 DEV queries the CIs are wide, so the choice is largely driven by the memory tie-breaker and must not be described as a clear quality win.

## D-042 Agent-efficiency experiment controls (2026-10-09)
10 tasks x 2 conditions (with and without the MCP server), Claude Code with a fixed version and model, same files, no CLAUDE.md/AGENTS.md/memory mentioning lodestone, fresh session and clean clone at the pinned tag per run, alternating condition order, per-task success criteria written and committed BEFORE any run, version/model/date recorded per run, grading not blind (limitation). Exploratory, capped at 20 runs.

## D-043 Multi-language smoke test (2026-10-09)
One popular public repo per tier-1 language (TypeScript, Python, Java), each pinned by commit sha; about 10 hand-written queries and expected files per repo, written and committed BEFORE lodestone is first run on that repo; labeled a smoke test, not a benchmark. Repos and shas: PENDING (P2-S8-T07).

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
## D-053 Inference runtime: in-process cgo versus managed subprocess (PENDING P2-S0-T03)
## D-054 Go MCP SDK (PENDING P2-S0-T04)
## D-055 GGUF sources, quantization, probe results, reference-cosine threshold (PENDING P2-S0-T05)
## D-056 Phase 2 embedding model and dimension, applying D-041 (PENDING P2-S0-T01)
## D-057 int8 decision from the compactness ablation on DEV (PENDING P2-S8-T02)

## Deleted decisions
- D-013 (ANN recipe with iterative scan, exact re-sort and slice post-filter): superseded by D-029 (slice-only embeddings, no post-filter in SQL). The ANN overlap gate itself is kept in D-027.
- D-016 (embedding variants with whole-repo header vectors): superseded by D-029.
- D-021 (cut order in Phase 1): replaced by the Phase 1 priority list in ROADMAP.md.
- D-026 (capacity assumption): deleted with all hour estimates; also removed: weekend mapping, capacity discussion, velocity rules.
- The third embedding candidate (provisional) and its comparison configuration E3, both from the earlier plan: removed from scope; the registry keeps two models (D-003).
