# P2-S8 - Phase 2 evaluation

## Goal
Measure what Phase 2 actually delivers: retrieval parity with Phase 1 on Kubernetes, the compactness ablation, resource use, a multi-language smoke test, and an agent-efficiency experiment. Report honestly, including losses.

## Entry criteria
P2-S1 to P2-S7 done. Phase 1 TEST file hash verifies (`eval freeze --verify`). The parity index exists (`lodestone index --config eval/phase2/k8s-parity.toml`).

## Links
[eval-protocol.md#phase-2-parity](../eval-protocol.md#phase-2-parity), [#phase-2-compactness](../eval-protocol.md#phase-2-compactness), [#phase-2-resources](../eval-protocol.md#phase-2-resources), [#memory-measurement](../eval-protocol.md#memory-measurement), [#phase-2-smoke-test](../eval-protocol.md#phase-2-smoke-test), [#phase-2-agent-efficiency](../eval-protocol.md#phase-2-agent-efficiency), [#statistics](../eval-protocol.md#statistics), [#freeze](../eval-protocol.md#freeze); [v2-architecture.md#resource-targets](../v2-architecture.md#resource-targets); decisions D-037, D-038, D-041, D-042, D-043, D-057.

## Task order
Harness and DEV decisions (T01-T03) before any TEST run; resources (T04-T06); smoke test (T07 before T08); agent efficiency (T09 before T10, then T11); TEST runs (T12) after D-057 is recorded; report (T13) last.

## Tasks

### P2-S8-T01 - Phase 2 engine adapter for the eval harness
- Why: Phase 2 results must use the same harness, metrics and guards as Phase 1.
- Depends on: none
- Touches: `cmd/eval/engine_lodestone.go`, `internal/evalconfig/`
- Details: register configs `P2-float` (float32 vectors at the model's native dimension) and `P2-compact` (D-056 dimension, int8). Each runs `Engine.Search` from `internal/engine` on a lodestone index selected by `--index <path>` and applies the slice filter through the shared collapse helper before dedupe and cut (D-039). Result JSON carries the same fields as Phase 1 plus `engine: lodestone`, `lodestone_version`, `model`, `dim`, `vector_format`. `--split test` still requires `--final` and the TEST hash check (D-037).
- Acceptance: `go run ./cmd/eval run --config P2-compact --index <path> --split dev` writes `eval/results/P2-compact/dev.json` with 15 per-query rows; a unit test shows the slice filter runs before the cut (fixture: top-10 raw hits all outside the slice, 2 inside); `--split test` without `--final` exits non-zero.
- Tests: unit tests with a fake engine; the filter-before-cut test.
- Out of scope: running on TEST.
- Owner: agent

### P2-S8-T02 - Compactness ablation on DEV and the int8 decision
- Why: decide int8 by DEV before its single TEST run (D-041, D-057).
- Depends on: P2-S8-T01
- Touches: `eval/results/{P2-float,P2-compact}/dev.json`, `docs/plan/decisions.md` (D-057)
- Details: build two parity indexes (`--vector-format float32` at native dim, and int8 at the D-056 dimension), run both on DEV, `eval compare --base P2-float --cand P2-compact --split dev`. Apply the rule: use int8 unless DEV shows an nDCG@10 loss whose paired-bootstrap CI excludes 0. Record the outcome, the numbers and the CI in D-057; the shipped default is `P2-compact` when int8 is accepted, otherwise `P2-float`.
- Acceptance: both DEV JSON files exist; D-057 contains nDCG@10 for both, the CI of the difference, and the branch of the rule applied.
- Tests: unit test of the rule function on three synthetic CIs.
- Out of scope: TEST runs.
- Owner: agent

### P2-S8-T03 - Parity on DEV against A3n and A1
- Why: the main Phase 2 claim is measured during development on DEV.
- Depends on: P2-S8-T02
- Touches: `eval/results/phase2/parity-dev.json`
- Details: `eval compare` of the default Phase 2 config against `A3n` and against `A1` on DEV with paired-bootstrap CIs, overall and per route (flag wide CIs). Record where Phase 2 is worse.
- Acceptance: `parity-dev.json` has both comparisons with CIs per metric and route; the report section generator (T13) can render it.
- Tests: none.
- Out of scope: TEST.
- Owner: agent

### P2-S8-T04 - Resource measurements: idle, search, index
- Why: the memory and size targets are claims that need numbers ([#phase-2-resources](../eval-protocol.md#phase-2-resources)).
- Depends on: P2-S8-T02
- Touches: `eval/results/phase2/resources.json`, `scripts/measure-mem.sh`
- Details: using `scripts/measure-mem.sh` (physical footprint on macOS, D-038) record: idle `lodestone mcp` footprint with no model loaded (after `initialize` plus 10 seconds); search-mode peak over 50 mixed queries on the Kubernetes parity index (including model load); index size on disk; first-index wall time and incremental no-change wall time on Kubernetes and on each smoke repo (after T08 provides them, append). Report RSS alongside as a second number.
- Acceptance: `resources.json` has `idle_footprint_bytes`, `search_peak_footprint_bytes`, `index_size_bytes`, `first_index_seconds`, `incremental_seconds`, with the method name `physical-footprint` in a `method` field; targets (under 50MB idle, under 700MB search) are shown next to the measured values with pass/fail.
- Tests: none.
- Out of scope: heavy mode.
- Owner: agent

### P2-S8-T05 - Heavy-mode peak per model
- Why: "heavy-mode peak documented per model".
- Depends on: P2-S8-T04
- Touches: `eval/results/phase2/resources-heavy.json`
- Details: measure physical footprint peak for (a) `chat` with the 3B model and (b) with the 7B model through the managed runtime, one 5-turn scripted session on the Kubernetes parity index, and (c) `index --summaries` on a 200-function sample for each. Same method as T04.
- Acceptance: `resources-heavy.json` has four peaks (two models x chat/summaries) with the method field.
- Tests: none.
- Out of scope: quality comparison (Phase 1 S9).
- Owner: agent

### P2-S8-T06 - Linux cgroup smoke job in CI
- Why: show default mode does not exceed a memory cap on Linux (D-038).
- Depends on: P2-S8-T04
- Touches: `.github/workflows/ci.yml`, `scripts/measure-mem-linux.sh`
- Details: a Linux amd64 job builds `lodestone`, runs `index` and 5 `search` queries on `testdata/fixture-repo` (with the real embedding model cached via the Actions cache or the fake embedder if the model download is too large for CI, recorded in the job comment) inside a container with `--memory=2g`, and prints cgroup `memory.peak`. The job fails if the container is OOM-killed or the peak exceeds 2GB.
- Acceptance: the CI run shows the `memory.peak` value; a deliberately lowered cap (`--memory=20m`) in a local run makes the job fail (recorded in Results).
- Tests: the CI job.
- Out of scope: macOS CI memory checks.
- Owner: agent

### P2-S8-T07 - [human] Write smoke queries and expected files BEFORE the first run
- Why: the sanity set must not be shaped by tool output (D-043).
- Depends on: none
- Touches: `eval/phase2/smoke/<repo>.json` (3 files), `docs/plan/decisions.md` (D-043)
- Details: choose one popular public repo per language (TypeScript, Python, Java), pin each by commit sha in D-043. For each, write about 10 sanity queries and the expected file(s) by reading the repo yourself; mix lexical, structural and semantic phrasing. Commit the files and the pinned shas BEFORE running lodestone on that repo. Label this a smoke test, not a benchmark.
- Acceptance: three JSON files with about 10 queries each and non-empty expected files; `git log` shows the commit precedes any lodestone index of those repos (the agent in T08 verifies by checking that no `.lodestone/` exists for the pinned checkouts at start); D-043 lists repo URLs and shas.
- Tests: none.
- Out of scope: running the tool.
- Owner: human

### P2-S8-T08 - Run the multi-language smoke test
- Why: evidence that any repo indexes and answers sensible queries.
- Depends on: P2-S8-T07, P2-S8-T04
- Touches: `eval/results/phase2/smoke.json`
- Details: clone each repo at its pinned sha into a scratch dir, run `lodestone index` (record errors, wall time, footprint, index size) and run each query through the engine; a query passes if any expected file is in the top 10. Record per repo: indexing errors count (must be 0 aborts), pass count out of about 10, and the failing queries with the returned files.
- Acceptance: `smoke.json` holds per-repo results with a `kind: smoke-test` field; every repo indexed without aborting; the report states pass counts without CIs or benchmark language.
- Tests: none.
- Out of scope: tuning anything after seeing results.
- Owner: agent

### P2-S8-T09 - [human] Agent-efficiency tasks and success criteria, before any run
- Why: success criteria written before runs avoid hindsight (D-042).
- Depends on: P2-S6-T08
- Touches: `eval/phase2/agent/tasks.md`, `eval/phase2/agent/protocol.md`
- Details: write 10 coding-agent tasks on the Kubernetes checkout at the pinned tag (find a definition, find callers, locate where behavior X is implemented, answer a how-does-it-work question with file references, ...), each with a written success criterion (what files or facts the answer must contain). Write the protocol with the controls: identical setup except the MCP server; same Claude Code version and model; same files; no CLAUDE.md, AGENTS.md or memory that mentions lodestone; fresh session per run on a clean clone; alternating condition order (with, without, without, with, ...); record version, model and date per run; grading is not blind and that is a stated limitation. Commit both files.
- Acceptance: both files committed; `git log` shows them before the first run record; each of the 10 tasks has a non-empty success criterion.
- Tests: none.
- Out of scope: running.
- Owner: human

### P2-S8-T10 - [human] Run the agent-efficiency sessions
- Why: collect the 20 runs under the protocol.
- Depends on: P2-S8-T09
- Touches: `eval/phase2/agent/runs/*.json`
- Details: 10 tasks x 2 conditions = 20 sessions following the alternation order in the protocol; save the transcript path, Claude Code version, model, date, condition, tool calls, tokens, and your success grade (0/1) per run. Stay within the subscription limits; stop and report if limits block completion (partial results are reported as partial).
- Acceptance: 20 run records (or a documented stop point), each with version, model, date, condition and grade; the alternation order matches the protocol.
- Tests: none.
- Out of scope: analysis.
- Owner: human

### P2-S8-T11 - Agent-efficiency analysis
- Why: turn the runs into an honest comparison.
- Depends on: P2-S8-T10
- Touches: `cmd/eval/agent.go`, `eval/results/phase2/agent.json`
- Details: `eval agent-report` reads run records and reports per condition: success rate, mean tool calls, mean tokens; paired differences per task with a bootstrap CI (10,000 seeded resamples), flagged as exploratory with n = 10 tasks.
- Acceptance: `agent.json` has both conditions, the paired differences with CIs and a `limitations` list (not blind, n = 10, single agent); unit test on synthetic runs.
- Tests: unit tests.
- Out of scope: new runs.
- Owner: agent

### P2-S8-T12 - Single TEST runs for the Phase 2 configurations
- Why: each new configuration runs on TEST exactly once (D-037).
- Depends on: P2-S8-T03, P2-S8-T02
- Touches: `eval/results/{P2-float,P2-compact}/test.json`, `eval/results/phase2/parity-test.json`
- Details: `eval freeze --verify`, then `eval run --config P2-float ... --split test --final` and `P2-compact` once each; compare each against A3n and A1 (paired bootstrap CIs, overall and per route, flagged wide). No parameter changes after D-057.
- Acceptance: both `test.json` files have 35 per-query rows and the Phase 1 `query_file_sha256`; a second `--final` run is refused; `parity-test.json` holds the comparisons.
- Tests: none.
- Out of scope: reruns to improve numbers.
- Owner: agent

### P2-S8-T13 - Phase 2 report section
- Why: honest write-up inside `eval/report.md`.
- Depends on: P2-S8-T12, P2-S8-T11, P2-S8-T08, P2-S8-T05, P2-S8-T06
- Touches: `cmd/eval/report.go`, `eval/report.md`
- Details: generated Phase 2 section: parity table (DEV and TEST, with CIs, per route), compactness ablation, resources (with method names and target pass/fail), smoke-test pass counts (labeled smoke test), agent-efficiency (labeled exploratory). It states in prose that the model and dimension choice used only Phase 1 DEV results with wide CIs (15 queries), so the choice is largely driven by the memory tie-breaker and is not a demonstrated quality win. No numbers are typed by hand.
- Acceptance: `eval report` twice is byte-identical; the section contains the sentence about wide CIs; `grep -c "not yet measured" eval/report.md` is 0.
- Tests: golden report test extended.
- Out of scope: the tool README.
- Owner: agent

## Measurements to record
All JSON under `eval/results/phase2/` and `eval/results/{P2-float,P2-compact}/`, plus D-043 (repo shas), D-057 (int8), rendered in `eval/report.md`.

## Exit criteria
Every Phase 2 measurement in the eval protocol exists and is in the report, including unfavorable ones; each new config has exactly one TEST run; ROADMAP updated.

## Risks + mitigations
- Subscription limits block the agent experiment: partial results reported as partial; it is the first item to drop if Phase 2 must shrink.
- Parity worse than A3n: reported with CIs and per-route breakdown; no tuning on TEST.
- Smoke repos change: pinned shas.

## Demo check
`cat eval/report.md` (Phase 2 section) and `cat eval/results/phase2/resources.json`.

## Results
(empty)
