# Decisions (ADR-lite)

Format: ID, date, decision, why, alternatives rejected. PENDING entries are filled by the named task. IDs are never renumbered or reused. Deleted decisions are listed at the end.

## D-001 Stack fixed (2026-10-08)
Go only; Postgres+pgvector (Phase 1); Ollama (Phase 1); Groq at query time only (Phase 1). Why: reproducible locally at $0. Rejected: Python tooling, hosted vector DB, hosted embeddings.

## D-002 Ollama runs natively (2026-10-08)
Why: Docker on macOS cannot use Metal. Rejected: containerized Ollama (CPU only).

## D-003 Keep the early-built prefix registry, normalization and probe (2026-10-08, revised 2026-10-09)
Built during setup, kept but UNVERIFIED until S0-T05/T07. The registry holds exactly two models: `qwen3-embedding:0.6b` and `nomic-embed-text`. Per-model verification record (card URL, exact query and document prefix, native dim, MRL yes/no, max context): PENDING (S0-T05). Rejected: rebuilding from scratch.
Outcome (S0-T05, 2026-10-10, Hugging Face model cards + Ollama library pages, fetched 2026-10-10):
- `qwen3-embedding:0.6b` — card https://huggingface.co/Qwen/Qwen3-Embedding-0.6B (secondary: https://ollama.com/library/qwen3-embedding:0.6b). Query format quoted from the card's canonical `get_detailed_instruct`: `Instruct: {task_description}\nQuery:{query}` — NO space after `Query:` (the card's separate TEI curl example shows `\nQuery: ` with a space; the code helper is taken as authoritative, matching the ticket's stated format). Pinned task sentence: `Instruct: Given a search query, retrieve relevant code\nQuery:` (one fixed sentence). Documents UNPREFIXED (card: "No need to add instruction for retrieval documents"). Native dim 1024 (user-definable 32–1024). MRL: yes. Maximum context: 32K. Embedding context size `num_ctx`: 4096 (32K > 4096, rule in architecture.md#ollama-requests).
- `nomic-embed-text` — card https://huggingface.co/nomic-ai/nomic-embed-text-v1.5 (secondary: https://ollama.com/library/nomic-embed-text). Query prefix quoted: `search_query: `; document prefix quoted: `search_document: `. Native dim 768. MRL: yes (Matryoshka Representation Learning). Maximum context: card's dimensionality table `SeqLen 8192`, but the card also states it "natively supports scaling of the sequence length past 2048 tokens" and the Ollama library page lists a 2K context window. `num_ctx`: 4096 by the card-maximum rule (caveat: if the S0-T07 `truncate:false` probe errors, drop to the native 2048, giving a 0.8x2048 ≈ 1638-token chunk ceiling).
- Document title rule: a non-empty `Document.Title` (file path + qualified symbol name, architecture.md#stage-embed / S3) is prepended to the text as its own line separated by a single `\n`, under the model's document prefix; an empty title renders the text alone. The previous `Title="none"` substitution is removed.
- sha256 of each rendered template for fixed inputs (query `pod eviction`; doc text `func evictPod()`; title `pkg/kubelet/kubelet.go (*Kubelet).evictPod`): qwen3 query `f44facdd632c133383e6f36cc06c141f9f16776d97926ee800046edc40001e6a`; qwen3 doc no-title `f9c00a192adb9bc11cd97b3c420b90e0564ec31f3d0676d4ec0d88ed2186893d`; qwen3 doc with-title `99a44a99894d86b7b56aa3069fdf864461be551741c306426b5bc9504275384c`; nomic query `e1ce55bdb8c19ba3bc53a73c71aa5cf2869d5c9415cda10da19dda0defcb171f`; nomic doc no-title `7de036e71c1bb500bad38d85ad0bfd141cd3fa5b284430cd5e6ab5840aca29d0`; nomic doc with-title `886261933d0f40c5aaa60799ad69f2a33b9b82f38167a44afa39f59997e37b92`.
- Prefix contradiction check: no prefix string assumed anywhere in docs/plan contradicts the cards (`grep -rniI 'Instruct:\|Query:\|search_query\|search_document' docs/plan` finds no pinned prefix), so no new superseding decision is added; this outcome completes D-003.
Outcome (S0-11, 2026-10-10): Qwen3 query format kept as `Instruct: {task}\nQuery:{query}` (no space after `Query:`). The model card contradicts itself: its `get_detailed_instruct` helper has no space, its TEI curl example has one; the helper is the model's reference implementation and is kept. Which variant retrieves better is an evaluation question, not decided here. Rendered-template hashes above are now pinned by `TestRenderedTemplateHashesArePinned`; a one-character change to any of the four templates fails `TestRequestBodies`/`TestRenderedTemplateHashesArePinned` (mutation transcript in the S0 sprint Results). The probe's unit-norm range is 0.99 to 1.01 (measured 1.0000 for both models).
Outcome (S0-12, 2026-10-10, live Ollama 0.40.1, `/api/embed` with `truncate:false`, filler text of about 5 bytes per token): the nomic caveat is resolved, the embedding `num_ctx` for `nomic-embed-text` is 2048, not 4096. Ollama clamps the requested size to the model's own 2048-token context: nomic accepts 1,909 tokens and rejects about 2,860 tokens with `num_ctx` unset, 2048, 4096 and 8192 alike, and `num_ctx` 1024 rejects 1,432 tokens. `qwen3-embedding:0.6b` honors the request: with `num_ctx` unset it accepts 3,812 and rejects about 4,200; with 8192 it accepts 4,574; with 1024 it rejects 2,288; its `num_ctx` stays 4096. The embed client now sends `options.num_ctx` from the registry on every request (D-015), and `doctor --ollama-checks` checks per pulled model that an input at 0.8 x `num_ctx` (estimated, bytes/4) is accepted and one at 1.5 x is rejected, failing otherwise. Consequence for S3: a nomic request may be at most 0.8 x 2048 = 1,638 estimated tokens including header, title and prefix, below the 1,500-token chunk ceiling plus header; the chunk limit (one for all models or per model) is decided at S3-T01 and recorded here.
Superseded in part by D-062: the nomic `num_ctx` of 4096 above is wrong (it is 2048); read the S0-12 outcome and D-062.

## D-004 `chunks.search_tsv` becomes a regular column (2026-10-08)
A generated column cannot split identifiers. New migration 00002; populated from Go in the chunk stage (S3). Rejected: editing 00001; a SQL splitting function.

## D-005 Slice FROZEN at the end of S1; size by machine-time compute budget (2026-10-08, revised 2026-10-10)
Summary compute budget: two overnight runs of machine time (about 20h). Eligible-function budget = min(25k, measured throughput x 20h x 0.8), throughput measured in S1 on 200 post-skip functions (body of at least 3 lines, D-014) plus about 20 file-level prompts, combined across function, file and package levels. If the full slice has more eligible functions than the budget, drop client-go first, then pkg/controller subpackages by size, never pkg/scheduler, until it fits. The slice never grows beyond this budget. Before the freeze, S1-T12 probes the mining yield on the candidate slice (need at least 30 candidates in tier 1 plus tier 2). If the yield is short, in order: (1) change the composition within the same budget (for example keep client-go and drop more pkg/controller subpackages), (2) extend the mining window further back. The KEP cap (5 of 25) stays. Never changes after S1 (it is the eval scope). Size, calculation, yield-probe outcome and the sha256 of the sorted in-slice path list: PENDING (S1-T13); `indexer walk --verify-slice` (`make verify-slice`, run in S8-T05 and S10-T02) compares the hash. The 20h figure is the only hour budget in the plan; measured throughput is reported as a metric.

## D-006 Pinned Kubernetes tag (2026-10-08)
The latest stable `v1.N.0` whose `go` directive is at most the local Go toolchain (local: go1.27.1). Never changed. Chosen tag v1.37.0 (commit f54c212e3a2f75d674b717a9b29052b20b60aefc, committed 2026-08-26), `go` directive `1.26.0` <= local go1.27.1.

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
Outcome (S0-T07, 2026-10-10, `doctor --ollama-checks`): two identical 7B generation requests returned the same prompt-token count (gen_count_1 = 36, gen_count_2 = 36), so no prompt-cache reduction was observed; the estimate is still not compared with `prompt_eval_count`.

## D-017 AGENTS.md links are plain, not auto-loaded imports (2026-10-08)
`AGENTS.md` mentions `docs/agents/*` as backticked paths, not `@`-imports, so they are read on demand. S0-T09 re-checks that no `@` import exists, re-points `docs/agents/domain.md` to `docs/plan/glossary.md` and `docs/plan/decisions.md`, and fixes the stale "Domain docs" line in `AGENTS.md`.
Outcome (S0-T09, 2026-10-10, `grep -rn "@" AGENTS.md | grep -v "@latest"` returns nothing): no auto-loading imports exist. `AGENTS.md` is the only project instruction file (there is no `CLAUDE.md` in this repo), and it references `docs/agents/*` only as plain backticked paths.

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
`go list -deps ./pkg/scheduler/...` must succeed inside the clone with the network disabled. Starts from the workspace load (D-071), not `GOWORK=off -mod=vendor`.
Working invocation (S2 ticket 01, refs D-071): inside `data/kubernetes`,
`GOOS=linux GOARCH=amd64 GOWORK=auto GOTOOLCHAIN=local CGO_ENABLED=0 go list -deps ./pkg/scheduler/... ./pkg/kubelet/... ./pkg/controller/...`
(client-go is dropped from the frozen slice, D-070). The load is read-only: it builds SSA and VTA in memory and writes no database row. The offline attempt with `GOPROXY=off GOFLAGS=-mod=readonly` FAILS: `k8s.io/klog/v2@v2.140.0` is not in the module cache (`module lookup disabled by GOPROXY=off`), so the workspace load needs the network for that dependency and Q-05 stays open with that reason.

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
`GOOS=linux`, `GOARCH=amd64`, `CgoEnabled=false` in both the S1 walk filter and the S2 `callgraph-gt` load (plus `GOTOOLCHAIN=local` for `callgraph-gt`). Why: tree-sitter indexes every file it is given, `go/packages` loads only one build context; unequal file sets make call-graph precision/recall and structural labels incomparable. Acceptance: the slice `.go` file set from `walk` equals the set loaded by `go/packages` (S2-T06). Rejected: host build context (darwin).
Amended 2026-10-13 (refs D-071): the `GOWORK=off`/`-mod=vendor` part was dropped because that load fails at the pinned tag (no vendored client-go); `callgraph-gt` loads under the workspace. `GOOS`, `GOARCH` and `CGO_ENABLED` are unchanged and the walk and `go/packages` file sets stay equal (D-067: 876 files, 720 packages, 0 errors). The working invocation is recorded in D-025 by S2 ticket 01.

## D-059 Ignore-file library (PENDING P2-S3-T02)

## D-060 Ollama model IDs recorded at S0; any ID change before S8 means re-running affected DEV configs (an embedder change means re-embed); never mix IDs within one result (2026-10-10)
Ollama model IDs recorded at S0; any ID change before S8 means re-running affected DEV configs (an embedder change means re-embed); never mix IDs within one result. Recorded from `ollama list` / `/api/tags` on 2026-10-10 (Ollama 0.40.1):
- `qwen3-embedding:0.6b` — digest `ac6da0dfba84a81fdbfbaf330198c33cd77c4cdfc53e8bc50eb581914a15621d`, size 639,150,858 bytes (~639 MB), dim 1024 (configured `OLLAMA_EMBED_MODEL`).
- `nomic-embed-text:latest` — digest `0a109f422b47e3a30ba2b10eca18548e944e8a23073ee3f3e947efcf3c45e59f`, size 274,302,450 bytes (~274 MB), dim 768.
- `qwen2.5-coder:7b` — digest `dae161e27b0e90dd1856c8bb3209201fd6736d8eb66298e75ed87571486f4364`, size 4,683,087,561 bytes (~4.7 GB), summary model.
Evidence (S0-T07, 2026-10-10): `doctor --probe-all` printed `nomic-embed-text dim 768 norm 1.0000 PASS` and `qwen3-embedding:0.6b dim 1024 norm 1.0000 PASS`, exit 0; `doctor --ollama-checks` printed `gen_count_1 36`, `gen_count_2 36`, and `embed_oversized: error` for both embedding models, exit 0.

## D-061 S0 process: one branch, one commit per ticket, draft PR for CI (2026-10-10)
Work happens on branch `s0-setup-verification`. One commit per ticket. Agents push only when CI proof is needed and after showing the owner the workflow triggers and GitHub auth status; a draft pull request triggers CI. The owner reviews and merges; agents never push to or merge into `main`.
Superseded by D-069 (work directly on main; no pushing).

## D-062 Embedding context sizes and the Qwen3 query format supersede the values first recorded in D-003 (2026-10-10)
Supersedes, in D-003's S0-T05 outcome: `nomic-embed-text` embedding `num_ctx` 4096 (now 2048) and the unpinned Qwen3 query format. Why: S0-12 measured on live Ollama that nomic rejects input over about 2048 tokens whatever `num_ctx` is requested (Ollama clamps to the model's own 2048 context, although the model card lists 8192), while `qwen3-embedding:0.6b` honors the request and keeps 4096. The embed client sends `options.num_ctx` from the registry on every request (D-015), and `doctor --ollama-checks` verifies per pulled model that an input at 0.8 x `num_ctx` (estimated, bytes/4) is accepted and one at 1.5 x is rejected. A nomic request may therefore be at most 0.8 x 2048 = 1,638 estimated tokens including header, title and prefix; the chunk ceiling that follows is decided at S3-T01 and recorded in a new entry. The Qwen3 query format stays `Instruct: {task}\nQuery:{query}` (no space after `Query:`), following the card's reference helper over its curl example; which variant retrieves better is an evaluation question. Rejected: relying on the model card's 8192 for nomic; leaving `num_ctx` unset.

## D-063 `make ci-local` substitutes for CI until the owner pushes; toolchain CGO is separate from the corpus build context (2026-10-10)
S1 starts from "S0 signed off locally", not "CI green": the branch is not pushed (D-061). `make ci-local` mirrors the commands and environment of `ci.yml` against the compose Postgres (`go vet`, `REQUIRE_TEST_DB=1 go test -v -count=1 ./...` with the SKIP check, `make lint`, `make lint-ci`); actionlint runs through a pinned `go run`. Pushing a draft PR for CI proof is an owner decision on the S1 sign-off checklist; the unobserved CGO CI change is the recorded risk. `CGO_ENABLED=1` is set only for building and testing (tree-sitter needs it). The corpus build context stays `CGO_ENABLED=0` (D-058): `walk` sets `CgoEnabled=false` on its `go/build.Context` explicitly and `callgraph-gt` appends `CGO_ENABLED=0` to the `go/packages` environment, never inheriting it. Tests: with `CGO_ENABLED=1` in the process environment, walk's context has `CgoEnabled == false` and the `callgraph-gt` environment ends with `CGO_ENABLED=0`. Rejected: a job-wide `CGO_ENABLED` in `ci.yml`; treating S0's deferred CI proof as satisfied.
Updated by D-069: nothing is ever pushed, so `make ci-local` is the only CI check and there is no draft-PR decision.

## D-064 S1 is done when the overnight run is launched, not completed (2026-10-10)
S1 done = slice FINAL with the sha256 of the sorted slice file list recorded at T13, the quality gate passed (at most 3 bad of 30, D-068), the owner has started `make summarize-run`, two `summarize-status` readings show `done` strictly increasing, and pause and resume are verified by counts (start, wait until `done >= start + 5`, SIGINT, assert no partial rows, resume, `done` increases, 0 duplicate `(level, target_id, prompt_version)`). The agent never starts the full run. S2 to S4 proceed while it runs (pausing only for S3-T09). Rejected: waiting for the run to finish before S2; wall-clock pass criteria.

## D-065 Stable symbol ids; per-package delete of missing symbols (walk rehomes, parse sweeps orphan summaries); summary freshness by input_hash (2026-10-10)
`symbols.id` must survive a re-parse because `summaries.target_id` has no foreign key and a 7B summary run is expensive. Parse upserts on `qualified_name` (`ON CONFLICT DO UPDATE ... RETURNING id`) and, in one per-package transaction (all upserts first, S1C-11), deletes symbols of the package's files that were not seen anywhere in the package parse (`id <> ALL($seen)`) together with their function-level summaries. A summary is fresh only if the `input_hash` recomputed from the CURRENT symbol equals the stored one; `target_id` alone is never trusted (a duplicate-suffix shift such as `init#1`/`init#2` changes which code a name points at). `make reset-db` must not run while summaries are to be kept (it restarts identity). Rejected: delete-then-insert per file (new ids orphan summaries silently). S1C-11: `walk.Sync` re-homes symbols of a vanished file to the first surviving file of the same package and clears that file's `parsed_sha`, `parse` sweeps function summaries left without a symbol, and selection requires `f.parsed_sha = f.sha` (`summarize.ParsedFileWhere`) so a re-homed symbol is not used before its package is re-parsed.

## D-066 Database tests use a throwaway database each (2026-10-10)
Outcome of the S0 review follow-up. `internal/testdb` creates `codesearch_test_<pkg>_<8hex>` (lowercase, at most 63 bytes, because Postgres truncates longer identifiers silently) on the server of `TEST_DATABASE_URL`, migrates it, and drops it with `WITH (FORCE)` on cleanup; it refuses to drop any name without the prefix. Tests never write to the database named in the URL and packages can run in parallel. An empty URL skips, except under `REQUIRE_TEST_DB=1` (set by `make test`, `make ci-local` and CI), which fails; CI and `make test` run one `go test -v -count=1 ./...` and fail on any `--- SKIP`. `make test-db-clean` drops leftovers. No raw `t.Skip` in database tests. Rejected: sharing the dev database; a store-only SKIP check.

## D-067 walk and go/packages file sets are proven equal before the freeze (2026-10-10)
Refines D-058: the equality check moves from S2-T06 into S1-T08 (`callgraph-gt --check`) and is re-run in T13 on the post-drop-list set, because the slice freeze is irreversible. Loads the four roots with `go/packages` (`NeedName|NeedFiles`, `Tests: false`, `GOOS=linux GOARCH=amd64 CGO_ENABLED=0` appended, `GOWORK=off`, `GOTOOLCHAIN=local`, `-mod=vendor`), client-go by import path from the root module, applies the shared `walk.Excluded` predicate to the loaded files, normalizes paths with `filepath.EvalSymlinks`. Exit 0 equal, 1 unequal (both directions, capped at 50 lines each, totals), 2 load error (recorded under Q-05, the owner decides). S2-T06 extends the same flag with offline loadability (D-025). Rejected: discovering a mismatch in S2.

## D-068 Summary quality gate and the single current prompt version (2026-10-10)
Before the overnight run, a seeded sample of 200 (stratified across the four roots in proportion to eligible-function counts, candidates ordered by `qualified_name` before the seeded shuffle) is summarized and 30 of its `done` rows (seed 3) are graded by the author. A summary is bad if it states behavior the code lacks, names an absent callee or behavior, restates the signature, contains code or preamble, omits the primary effect or return value, or is empty or cut off; the first failing criterion is recorded in `.scratch/S1/quality-gate-<prompt_version>.jsonl`. At most 3 bad passes (10%); 4 or more fail: revise the prompt, bump `prompt_version`, redo; a second failure stops for the owner. `too_large` and `truncated` counts among the 200 are reported separately. The current `prompt_version` is one constant; `summarize-status`, embedding (S3, S5) and coarse-to-fine retrieval filter on it, and a failed version's function-level rows are deleted at T14 after the new gate passes and before any summary embedding. Passing gate rows are kept by the full run (unchanged `input_hash`).

## D-069 Work directly on main; agents never push (2026-10-10)
Supersedes D-061 (branch per sprint, one commit per ticket, draft PR for CI proof, owner merges) and the push and draft-PR parts of D-063. The owner works on `main`: commits are local and made directly on `main`, one per ticket; there are no feature branches. Agents never run `git push`, never open a pull request and never upload anything to a remote, and they do not propose it as the way to get CI proof. CI is checked locally by `make ci-local` (and, for S0, by the simulation recorded in the S0 Results); the owner may push on their own, outside the plan. `AGENTS.md` carries the rule. Rejected: keeping the branch-and-draft-PR flow.

## D-070 Slice frozen (refs D-005) (2026-10-10)
The slice is FINAL, never changes. D-005 is not edited; this entry fills its PENDING size, calculation, yield-probe outcome and path-list sha256. A re-freeze needs an owner-authorized superseding entry. No CI hash pin (D-069: nothing is pushed).

**Tag / commit.** `v1.37.0`, commit `f54c212e3a2f75d674b717a9b29052b20b60aefc` (D-006).

**Ordered drop list (D-005 order).** `["staging/src/k8s.io/client-go"]`. The four slice roots (`pkg/scheduler`, `pkg/kubelet`, `pkg/controller`, `staging/src/k8s.io/client-go`) are unchanged; the drop list removes whole subpath prefixes and the slice never grows.

**Final size and calculation.** In-slice = **876 files**, **329 packages**, **187,823 LOC**. The candidate slice before the drop was 1,131 files / 423 packages / 234,075 LOC. D-005's eligible-function budget at the measured concurrency-1 throughput is `min(25k, 459.14 fn/h × 20 h × 0.8) = 7,346` eligible functions. The candidate slice held **8,225** eligible functions (S1-08), over budget, so the D-005 first drop (client-go) was applied; the frozen slice holds **6,673**, under budget. Dropping client-go removed 255 files, 94 packages and 1,552 eligible functions. Combined run-length projection (T13 formula, benchmark `eval/results/bench/summarize.json`): function `6,673/459.14 = 14.5 h` + file `876/167.33 = 5.2 h` + package `329/226.35 = 1.5 h` = **21.2 h at concurrency 1** (**17.4 h** at concurrency 2, `fn_per_hour_c2 = 622.49`); the S1 overnight run is function-level only (14.5 h) and fits the 16 h effective budget, while the file/package levels are S5.

**Eligible-function count.** In-slice `func`/`method` with `end_line - start_line >= 3`: **6,673** (in-slice `func`/`method` total 8,534).

**Yields recomputed on the frozen slice (from the request cache, 0 HTTP calls).** `yield_12m = 115`, `yield_18m_extension = 35` (total **150 >= 30**); `by_prefix = {pkg/controller 35, pkg/kubelet 67, pkg/scheduler 49, staging/src/k8s.io/client-go 0}` (client-go's 18 and the 2 controller PRs that also touched a dropped file are no longer in the slice); `windows_at_cap = 0`; `prs_scanned = 938`. A second run made 0 HTTP calls (cache mtimes unchanged).

**File-set check.** `go run ./cmd/callgraph-gt --check` exits **0** and prints `file-set: equal (876 files)` on the post-drop set.

**Path-list sha256.** `2738a003140d98239b54254051a0ddf0b46c9fd1b554ac6f4f6f43edc3c04ec5` over the sorted `in_slice` path list (repo-root-relative, `/` separators, one per line, LF-terminated, bytewise sort = `LC_ALL=C sort`), written to `internal/walk/slice.sha256`; it equals the shell recomputation (`docker compose exec -T postgres psql -tA -U codesearch -d codesearch -c "select path from files where in_slice" | LC_ALL=C sort | shasum -a 256`).

**Deviation from the ticket's 15,000-25,000 in-slice func/method expectation.** Not met (8,534, and 6,673 eligible), with the honest reason: this corpus at tag `v1.37.0` yields fewer hand-written non-generated `func`/`method` in the four roots than the ticket assumed. The walk excludes vendor, `zz_generated`, `.pb.go`, `testdata`, `_test.go`, `client_go_generated` and build-constrained files first, so the four roots contain only ~8.5k non-generated `func`/`method` total; the 15,000-25,000 figure is unreachable at this tag without counting generated or test functions, which the slice's purpose (hand-written code search) forbids. The eligible count (6,673) is what the summary budget and eval scope actually consume, and it is under the D-005 budget.

## D-071 Unrequested S1 scope: gate moved to `cmd/tools`; workspace fallback and rate-limit retry kept (2026-10-13)
The owner delegated four decisions on behavior the S1 spec did not ask for (S1C-06). (1) The one-off quality-gate helper moves from the production indexer to a new `cmd/tools` binary: `indexer gate` becomes `go run ./cmd/tools gate` (same flags, same output for the same seed; verified byte-identical for seed 3). Why: it is author tooling for one sprint, so it does not belong in the binary that ships; `cmd/tools` follows the one-binary-per-concern layout beside `cmd/eval` and `cmd/callgraph-gt` and the shared `internal/cli` table. No Makefile target or runbook invoked it; the S1 Results line was updated. (2) The workspace-mode fallback in `callgraph-gt --check` is KEPT. Why: verified against Q-05 and `cmd/callgraph-gt/check.go`: the pinned tag has no `vendor/k8s.io/client-go`, `api` or `apimachinery`, the pinned `GOWORK=off -mod=vendor` load fails with `inconsistent vendoring`, and the workspace (`GOWORK=auto`) load is what loads the slice (720 packages, 0 errors); removing it makes `--check` exit 2 on the real tag. Named in the command's package comment and flag help. (3) The retry once on a 403/429 rate-limited response in `internal/github` is KEPT and documented in the package comment. Why: it is cheap, tested on the injected clock, and the `mine-yield` probe (and S4-T05 later) depends on it to survive the core bucket limit during a long run. (4) The standalone ETA bug note: no action here; S1C-13 folds it into the sprint Results and deletes the file. Rejected: keeping `gate` in the indexer; removing the workspace fallback; dropping the retry.

## D-072 Corrected mining yield is 25 and is accepted (refs D-005, D-070) (2026-10-13)
S1C-01 fixed the probe: a pull request now counts only if every changed file is a non-test, non-generated Go file in the candidate slice (1 to 5 files) and it closed an issue (closing keyword plus issue reference in the PR body; the REST search can only express `linked:issue`). Re-run on 2026-10-13 against the cache of the 2026-10-10 run (938 PRs scanned, no new API calls, `make mine-yield`, 18-month window ending 2026-08-26): yield_12m=20, yield_18m_extension=5, total 25, windows_at_cap=0; by prefix: pkg/kubelet 10, pkg/scheduler 8, pkg/controller 7, client-go 0. Rejections: non-Go file 69, test file 237, generated file 6, outside slice 353, too many files 227, no changed files 0, no closing issue 21. D-070 recorded 150 (115 + 35) under the looser rule, which counted PRs that also touched non-Go files; that figure was overstated and D-070 stays unedited. 25 is below the 30 threshold. Owner decision: accept it. The slice, its hash (`make verify-slice` matches) and the 6,694 written summaries are unchanged. The evaluation question set is completed in S4 from the eval-protocol fallbacks (discard unclear or leaking issues, widen the window, up to 5 KEP/official-docs questions); no further mining is done in S1. Rejected: extending the window now (more API calls for no S1 benefit) and changing the slice composition (it would invalidate the summaries).

## D-073 Edge resolution vocabulary (2026-10-13, refs D-019, D-023)
`edges.resolution` gains a fourth value, `unresolved`, and `edges` gains `unresolved_reason text` with allowed values `external`, `no_match`, `unknown_receiver`, `func_value`, `candidates_capped`. Invariants (CHECK): `(resolution = 'unresolved') = (callee_id IS NULL)` and `(resolution = 'unresolved') = (unresolved_reason IS NOT NULL)`. `name_only` is a guess and always has a `callee_id`. Retrievers and the `callers`/`callees` tools follow only `callee_id IS NOT NULL`; unresolved rows are listed separately with their reason. `graph --stats` prints counts per `(resolution, reason)` and they sum to the total. Rule 5 (unknown receiver, same package only) stays; when it finds nothing the reason is `unknown_receiver`. Why: the earlier plan stored external and no-match callees as `name_only` with a NULL callee, which made a guess and an admission of failure indistinguishable. Rejected: keeping three values; dropping unresolved rows (recall accounting needs them). Migration is 00005 (00004 is taken by S1).
Reasons (amended 2026-10-13): `external` means the callee's import path has no row in the indexed filtered set (`files.package`): standard library, vendored, generated or otherwise excluded code. `no_match` means the package is indexed but no symbol matched, which is a parser or resolver limitation that S2 error analysis reads one by one. `unknown_receiver` is Rule 5 finding nothing. `func_value` is a call through a function-typed local, parameter or closure variable: deliberately not resolvable by tree-sitter (added 2026-10-13 so `no_match` stays a clean limitation bucket). Builtin calls (`len`, `cap`, `append`, `make`, `new`, `panic`, `recover`, `copy`, `delete`, `close`, `print`, `println`, `min`, `max`, `clear`, `complex`, `real`, `imag`, unless shadowed by a local symbol) and type conversions are not stored; `graph --stats` counts them as `skipped_builtin` and `skipped_conversion`, and stored rows plus skip counters reconcile with the call-site total.

## D-074 Call-graph scoring universe and identity (2026-10-13, refs D-005, D-029, D-070)
Precision and recall are over `(caller_id, callee_id)` pairs where caller and callee are both in the slice. Every tree-sitter edge from an in-slice caller falls in exactly one bucket: `scored_in_slice`, `resolved_out_of_slice`, or `unresolved(reason)`; the report prints all buckets and asserts they sum to the total. Out-of-slice resolved edges are neither true nor false positives. The report states the numbers are in-slice only and gives the out-of-slice share. `call_line` is used only for the unique-index dedupe (same-line duplicates collapse identically in both graphs; GT `call_line` is 0 when unavailable); a line-level comparison is at most secondary because SSA and tree-sitter disagree on multi-line calls. Why: GT only exists for the slice (D-005, D-029) and the frozen slice dropped client-go (D-070), so edges to indexed but out-of-slice code have no ground truth.

## D-075 Interface-candidate cap amends D-023 (2026-10-13)
Rule 4 (interface receiver) emits one `ambiguous` row per implementer (D-023) up to `MaxInterfaceCandidates`. Candidates are counted over the full indexed candidate set before any slice filtering. If the count exceeds the cap, store one row with `resolution='unresolved'`, `unresolved_reason='candidates_capped'`, `callee_id NULL` and `callee_raw` kept. The cap applies to Rule 4 only. Candidates are concrete types in the indexed set that declare every method name of the interface (value or pointer receivers; interfaces excluded); signatures are not compared. Same-package embedded interfaces are expanded recursively with a cycle guard; a cross-package or unresolvable embedded interface contributes only the names known (a superset) and is counted as `interfaces_partial`; methods promoted from embedded structs are not seen (consistent with T03); an interface with zero known methods gets no Rule 4 rows.
Choosing N (rule fixed before the data): `graph --stats --uncapped-histogram` runs the real code path with a no-op writer (no database writes) and prints p50/p90/p99/max plus counts at 5, 10, 20, 50, 100; the output is committed as `eval/results/callgraph/cap-histogram.json`. N is the smallest of {5, 10, 20, 50, 100} such that at least 95% of interface call sites have a candidate count <= N; if that is above 50, stop and escalate to the owner. N is never chosen from precision or recall. Value and histogram file: PENDING (S2 ticket 07).

## D-076 Graph freshness and edge FK (2026-10-13, refs D-019, D-073)
Migration 00005 changes `edges_callee_id_fkey` to ON DELETE CASCADE (`caller_id` already cascades); SET NULL would break the D-073 invariant. A tree-sitter edge also goes stale when other symbols appear or disappear (a `no_match` becomes resolvable, Rule 4 candidates and Rule 5 lookups change), so freshness is a fingerprint, not a per-file clear. `files.graphed_symbols_fp text` stores the fingerprint the file was graphed against: sha256 over `(id, kind, package, qualified_name, receiver_type)` of all indexed symbols ordered by id, plus a `resolver_version` constant; line numbers and body hashes are excluded. A file is stale iff `graphed_sha <> sha` or `graphed_symbols_fp IS DISTINCT FROM` the current fingerprint. `parse` does not touch graph state; `graph --status` reports staleness. Rejected: clearing `graphed_sha` for files that pointed at deleted symbols (misses every other kind of staleness); rebuilding everything on any parse change.
Amended 2026-10-13 (ticket 04): the fingerprint also covers the declared-name index, because Rule 2 and the builtin/conversion classification resolve against package-level declared names that are deliberately not stored as symbols (for example `type Foo int`, vars and consts). The fingerprint is now sha256 over the indexed symbols `(id, kind, package, qualified_name, receiver)` ordered by id AND the declared-name index `(package, name, kind)` ordered deterministically (packages by import path, names within a package, each with its kind), plus the `resolver_version` constant. Adding or removing such a declaration in one file therefore marks a file that resolves against it stale (`symbols changed`).
Amended 2026-10-14 (ticket 06): the fingerprint also covers the same-package facts Rule 3 now reads, so it is sha256 over the symbols, the declared-name index, the named struct field types `(package, struct, field, resolved type)` and the single-result constructor return types `(package, function, resolved type)`, each ordered deterministically. A qualified field or result type is stored as its resolved import path (resolved against the declaring file's aliases at extraction time), so changing a field's type, a constructor's result type, or the package an alias points to marks the files that resolve through it stale. Why: Rule 3 field chains and `x := NewT(...)` constructor typing are resolution inputs exactly like the declared-name index, so a change to either must not be silently reused.

## D-077 GT rows share the edges table (2026-10-13, refs D-011, D-073)
Ground-truth rows live in `edges` with `source='gopackages'`, `resolution='exact'` and `unresolved_reason NULL`; `exact` means resolved with certainty by the producing source, and `source` disambiguates. Every scoring query filters by `source`. Every retriever, tool and stats query filters `source='treesitter'`; a test (one GT row and one tree-sitter row for the same pair) proves that no non-eval path reads `source='gopackages'`: in S2 the paths are `graph --stats` and `graph --status`; the structural retriever does not exist until S6-T05, whose acceptance carries the same one-edge test. Rejected: nullable `resolution` for GT; a `ground_truth` resolution value.

## Deleted decisions
- D-013 (ANN recipe with iterative scan, exact re-sort and slice post-filter): superseded by D-029 (slice-only embeddings, no post-filter in SQL). The ANN overlap gate itself is kept in D-027.
- D-016 (embedding variants with whole-repo header vectors): superseded by D-029.
- D-021 (cut order in Phase 1): replaced by the Phase 1 priority list in ROADMAP.md.
- D-026 (capacity assumption): deleted with all hour estimates; also removed: weekend mapping, capacity discussion, velocity rules.
- The third embedding candidate (provisional) and its comparison configuration E3, both from the earlier plan: removed from scope; the registry keeps two models (D-003).
