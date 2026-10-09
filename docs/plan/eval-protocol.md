# Evaluation protocol (canonical)

Scope = the slice ([architecture.md#corpus-and-slice](architecture.md#corpus-and-slice)). Harness: `cmd/eval` (Go). Decision IDs `D-nnn` are in `decisions.md`.

## Query set

50 queries, file-level relevance labels. Files: `eval/queries.dev.jsonl` (15) and `eval/queries.test.jsonl` (35), same JSONL schema (defined in S4-T01).

| Route | DEV | TEST | Total |
|---|---|---|---|
| lexical | 3 | 8 | 11 |
| structural | 4 | 10 | 14 |
| semantic | 8 | 17 | 25 |

## Sources

- **Semantic (25):** mined from GitHub. Merged PRs closing an issue ("Fixes #N"), merged in the 12 months before the pinned tag (widen by 6 months if yield is short), changed non-test non-generated files are 1-5 and all in the slice and all existing at the pinned tag. Query = issue title + first paragraph (HTML comments from the issue template stripped; no other edit, recorded in `_meta`). Discard issues whose text names a changed file or function (leakage). Fallback tiers when yield is under 25: widen window, then allow 6-8 changed files, then KEP questions. The yield is probed in S1 before the slice is frozen; if it is short, the slice never grows beyond the D-005 compute budget: first change its composition within the same budget, then extend the mining window further back (KEP cap unchanged; outcome recorded in D-005). At most 5 KEP/official-docs questions, written BEFORE looking at code. For 6-8 file PRs each file is reviewed by hand; incidental changes (renames, plumbing, trivial call-site updates) get grade 1 with the reason recorded (D-024).
- **Structural (14):** symbols sampled with a fixed seed from slice symbols with at least one caller in the ground-truth graph; only symbols whose short name is unique in the slice (otherwise the query is written `<Receiver>.<Name>`); queries "who calls X" / "what does X call". At least 4 of the 14 target symbols whose ground-truth callers include interface-method or function-value calls (at least 1 in DEV, 3 in TEST). Seed and strata recorded in the query file header and S4 Results.
- **Lexical (11):** symbols sampled with a fixed seed; "find/where is X". About two thirds exact identifiers, one third partial/split fragments (e.g. "pod backoff"). Labeled `route=lexical` by intent even when phrased as fragments; the router confusion matrix measures disagreements.

## Labeling

Never use the system under test to find labels. Allowed: grep, gopls, reading code, the ground-truth graph. Record per label `how` (grep | gopls | read | gt-graph | pr-diff) and, for grade 1 on PR-sourced labels, a `note`. Grades: primary = 2, secondary = 1. Structural queries: grade 2 = files with direct callers (or callees); grade 1 = files at hop 2 in the ground-truth graph, capped at 15 files in total. If direct files alone exceed 15, keep the 15 with the most call sites as grade 2 and record the omitted count in `_meta`.

## Freeze

`eval/queries.test.jsonl` is committed and its sha256 recorded in `eval/queries.test.sha256` (linked from D-010 in `decisions.md`) at the end of S4, before any tuning. Only TEST is hashed; DEV stays editable. All tuning on DEV. TEST runs once per configuration (D-037), in S8 for Phase 1 and in S9 and P2-S8 for the later configurations, via `eval run --split test --final`, which refuses to run if the file hash differs from the recorded one.

## Retrieval metrics

Per configuration, overall and per route type: Recall@10, Hit@10, MRR, nDCG@10. "Relevant" = grade >= 1 for Recall, Hit, MRR. nDCG gains are 2 and 1 (linear); the ideal DCG uses the best 10 grades from the FULL label set of the query, not only retrieved labeled files. Rankings are file rankings ([architecture.md#result-contract](architecture.md#result-contract)); results are filtered to the slice before the file de-duplication and the top-10 cut, in both phases (D-039). Also report per-query recall for structural queries so no single query dominates.

## Statistics

Paired bootstrap over queries, 10,000 resamples, fixed seed, 95% percentile CI of the metric difference versus the named base configuration of each comparison (A0 for the ablation ladder; others as stated; D-022). CIs are mandatory in every comparison table. Per-route CIs are reported and flagged as wide (n = 8, 10, 17 on TEST). Results where the CI includes 0 are reported as "no demonstrated difference".

## ANN overlap

"ANN overlap@10": top-10 overlap between HNSW search and exact brute-force search over the slice, per embedding model, averaged over a query set. Gate: below 95% means tune `ef_search` before any ablation run. Measured in S3 on a seeded probe set of 30 queries drawn from doc-comment first sentences (DEV does not exist yet, D-027), again on all DEV queries in S4, and re-run in S8 per model.

## Configurations

| ID | Definition |
|---|---|
| B0 | keyword baseline, defined in [architecture.md#keyword-baseline-b0](architecture.md#keyword-baseline-b0) (D-030) |
| A0 | flat baseline: `chunk_raw` embeddings, no header, semantic only |
| A1 | A0 + context header (`chunk_header`) |
| A2 | A1 + hierarchical summaries (coarse-to-fine), 7B summaries |
| A3 | A2 + lexical + structural behind the router (rules, then Groq when not confident) |
| A3n | A3 with flat header semantic retrieval, rules-only router (no LLM), no summaries; fan-out + RRF below the threshold (D-031). Reference for Phase 2 parity |
| A4 | no router: always fan out + RRF |
| E1 | A3n with `qwen3-embedding:0.6b` (an alias of A3n, run once) |
| E2 | A3n with `nomic-embed-text` |
| M1 | A3n at 256 dimensions versus its native-dimension counterpart, per MRL-capable model (`M1-qwen3-256` vs E1, `M1-nomic-256` vs E2); MRL support verified on each model card |
| A2-3B | A2 with 3B summaries and the A2 weights (S9, D-036) |

Phase 2 configurations (`P2-float`, `P2-compact`) are defined in [#phase-2-parity](#phase-2-parity) and [#phase-2-compactness](#phase-2-compactness). Each configuration runs on TEST exactly once (D-037).

Results: `eval/results/<config>/<split>.json` plus generated `eval/report.md`. Every result JSON records config, split, query-file sha256, git commit, embedding model, Groq model ID, date, and ANN `ef_search`.

## System metrics

Latency p50/p95 per route; LLM calls and tokens per query; cache hit rate.

## Router metrics

Accuracy versus labeled route type, confusion matrix, percent rule-routed versus LLM-routed.

## Call-graph metrics

Tree-sitter edge precision and recall versus the go/packages VTA graph on the slice, broken down by call kind (plain function, concrete method, interface method, function value). Edges are compared as `(caller, callee)` symbol pairs. Precision counts one unit per call site: an ambiguous tree-sitter call is a true positive only if the ground-truth callee is among its candidates; report ambiguous-rate and candidate-set size separately.

## Indexing metrics

Functions summarized per hour (per level), wall time per stage, vector counts per `(model, target_kind)`, DB size.

## Q&A evaluation

15 TEST semantic queries (the first 15 ids after a seeded shuffle, seed 77, recorded in `answers.json._meta`), two systems: `/ask` (multi-hop) and single-shot RAG (D-018). Both use identical answer prompt constraints. Metrics: citation precision (valid citations / emitted citations); answer grade 0/1/2 by the author against a written rubric (committed before any answers are generated); hops used (`/ask` only); tokens and LLM calls per answer for both; quality reported alongside cost. The grading sheet shows only the answer text and citations in a seeded shuffled order, with no traces, hop counts or metadata. Optional LLM-as-judge is secondary, reported with agreement versus human grades.

## Reproducibility

Every result JSON records the Groq model ID and date. All router and Q&A calls use temperature 0. After the single TEST run and the Q&A TEST run (and judge, if run), the `llm_cache` export (gzipped JSONL) is committed to `eval/results/` so numbers can be re-derived without Groq. If the Groq model is retired or changed before S8, record the new ID in decisions.md and re-run all DEV numbers with it before the final TEST run (D-007).

## Memory measurement

Used by every memory claim (D-038). macOS: physical footprint (`footprint` tool or `phys_footprint`) summed over the whole process tree (for Ollama: the server and its `runner` children), not RSS, because RSS undercounts Metal/unified-memory buffers and mmapped weights. Linux CI smoke job: the container cgroup's `memory.peak`. Both numbers are reported; each README claim names the measurement behind it. "Usable on an 8GB machine" is an inference from these numbers, not a test on 8GB hardware. Peak is the maximum over samples taken at 1 second intervals plus the final value.

## Phase 2 parity

Run the Phase 2 default mode (D-031 semantics, D-056 model and dimension) through the same harness: `P2-float` and `P2-compact` on DEV during development and once each on the frozen TEST set (D-037). Report the delta versus A3n (the matching Phase 1 configuration) and versus A1, with paired-bootstrap CIs, overall and per route, flagged wide. The slice prefixes are applied inside the vector search (`PathPrefixes`) and again by the collapse helper before dedupe and cut (D-039). Written to `eval/results/phase2/parity-{dev,test}.json`.

## Phase 2 compactness

`P2-float`: float32 vectors at the model's native dimension. `P2-compact`: the D-056 dimension with int8 quantization. DEV first (decides int8 per D-041, outcome D-057), then TEST once each. Report nDCG@10, Recall@10 and index size for both.

## Phase 2 resources

Per [#memory-measurement](#memory-measurement): idle MCP server footprint with no model loaded (target under 50MB), search-mode peak (target under 700MB), heavy-mode peak per model (3B and 7B, chat and summaries), index size on disk, first-index and incremental wall time on Kubernetes and on each smoke repo. Written to `eval/results/phase2/resources*.json` with a `method` field. The Linux job records `memory.peak` under `--memory=2g`.

## Phase 2 smoke test

Three popular public repos (TypeScript, Python, Java) pinned by sha (D-043); about 10 hand-written sanity queries each with expected files, committed before the first run. Pass = an expected file in the top 10. Reported as pass counts and the failing queries, labeled a smoke test, not a benchmark. Written to `eval/results/phase2/smoke.json`.

## Phase 2 agent efficiency

Exploratory (D-042): 10 tasks on Kubernetes at the pinned tag, with and without the MCP server, 20 runs. Controls: identical setup except the MCP server; same Claude Code version, model and files; no CLAUDE.md, AGENTS.md or memory mentioning lodestone; fresh session and clean clone per run; alternating condition order; success criteria committed before any run; version, model and date recorded per run. Metrics: success, tool calls, tokens, paired differences with bootstrap CIs. Limitations: grading is not blind, n = 10 tasks, one agent. Written to `eval/results/phase2/agent.json`.

## Limitations (to be repeated in README)

Changed files in a PR are not always the only relevant files. Merged-before-tag PRs mean the fix is already in the pinned tag. Small n means wide CIs. One labeler. Residual style differences between the two Q&A systems. Groq model drift. Phase 2: the model and dimension choice rests on 15 DEV queries with wide CIs and is largely a memory tie-breaker, not a demonstrated quality win; the smoke test and agent experiment are exploratory.
