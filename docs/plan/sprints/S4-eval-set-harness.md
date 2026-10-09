# S4 - Eval set & harness

## Goal
Build the evaluation harness (metrics, bootstrap CIs, report), mine and label the 50 queries, freeze TEST by hash, add the B0 keyword baseline, and run A0, A1 and B0 on DEV.

## Entry criteria
S3 done (A0/A1 searchable, vectors indexed). S2 ground-truth graph populated (structural labels depend on it). `GITHUB_TOKEN` available to you (fine-grained, read-only public repos).

## Links
[eval-protocol.md](../eval-protocol.md) (all of: [#query-set](../eval-protocol.md#query-set), [#sources](../eval-protocol.md#sources), [#labeling](../eval-protocol.md#labeling), [#freeze](../eval-protocol.md#freeze), [#retrieval-metrics](../eval-protocol.md#retrieval-metrics), [#statistics](../eval-protocol.md#statistics), [#ann-overlap](../eval-protocol.md#ann-overlap), [#configurations](../eval-protocol.md#configurations)); [architecture.md#result-contract](../architecture.md#result-contract). Decisions: D-010, D-012, D-022, D-024, D-027, D-030, D-039.

## Working order
Harness tasks (T01-T04) first so labeling (human, the long pole) can overlap the summary run and later S5 work; mining tools (T05-T07) next; labeling T08-T13; freeze T14; B0 T15; baseline run T16.

## Tasks

### S4-T01 - Query file schema, loader, validator
- Why: one schema for DEV and TEST ([eval-protocol.md#query-set](../eval-protocol.md#query-set)).
- Depends on: none
- Touches: `internal/evalset/`, `cmd/eval/validate.go`, `eval/queries.dev.jsonl`, `eval/queries.test.jsonl` (empty, committed)
- Details: one JSON object per line: `{"id":"S-007","route":"lexical|structural|semantic","text":"...","source":{"type":"github-pr|kep|sampled","ref":"<PR url|KEP|seed>","fetched":"2026-..."},"labels":[{"file":"pkg/scheduler/...go","grade":2,"how":"grep|gopls|read|gt-graph|pr-diff","note":"..."}]}`. ID prefixes `L-` lexical, `T-` structural, `S-` semantic. First line may be a comment-object `{"_meta":{...seed,strata,tag}}`. `eval validate` checks: unique ids, route in set, at least one grade-2 label, grades in {1,2}, label files exist in `files` with `in_slice`, `how` in set, grade-1 PR labels with 6-8 file PRs have `note`, DEV/TEST counts per route against the table (reports shortfall without failing until `--strict`).
- Acceptance: `go run ./cmd/eval validate` exits 0 on empty files; with a fixture file containing a nonexistent label path it exits non-zero naming the line; `--strict` fails while counts are below 15/35.
- Tests: table tests for each validation rule; golden parse test.
- Out of scope: labels themselves.
- Owner: agent

### S4-T02 - Harness core and retrieval metrics
- Why: every number in the report comes from here.
- Depends on: T01
- Touches: `cmd/eval/run.go`, `internal/evalset/metrics.go`, `internal/evalconfig/`
- Details: `eval run --config A0 --split dev [--final]`. Config registry `internal/evalconfig` maps ID to a `Retriever` (A0/A1 now; later sprints add A2-A4, E*, M1); records config, commit, model, `ef_search`, query-file sha256, date, Groq model ID (empty for now). For each query call the config's retriever (limit 10), compute per query: Recall@10, Hit@10, reciprocal rank, nDCG@10 per [#retrieval-metrics](../eval-protocol.md#retrieval-metrics) (relevant = grade >= 1; gains 2/1; ideal DCG from best 10 grades of the FULL label set). Output `eval/results/<config>/<split>.json`: per-query rows (ranked files, per-metric values) + aggregates overall and per route. `--split test` is refused without `--final`; with `--final` it verifies the TEST sha256 against D-010 (T14) and refuses a second final run of the same config unless `--rerun-reason` is given (recorded in JSON).
- Acceptance: unit tests with hand-computed values pass (e.g. ranking [rel2, nonrel, rel1] vs labels {A:2,B:1,C:1} gives nDCG computed with ideal [2,1,1]); `go run ./cmd/eval run --config A1 --split dev` on a 3-query fixture file writes the JSON with the fields above; `--split test` without `--final` exits non-zero.
- Tests: metric unit tests (perfect, empty, ties, label set > 10), JSON golden for a fake retriever; refusal-path tests.
- Out of scope: bootstrap (T03), report (T04).
- Owner: agent

### S4-T03 - Paired bootstrap CIs
- Why: n is small, CIs are mandatory ([#statistics](../eval-protocol.md#statistics)).
- Depends on: T02
- Touches: `internal/evalset/bootstrap.go`, `cmd/eval/compare.go`
- Details: `Bootstrap(a, b []float64, resamples=10000, seed) (meanDiff, lo, hi)` resampling query indices jointly (paired), percentile 95% CI of mean(b)-mean(a). `eval compare --base A0 --cand A1 --split dev` prints per-metric and per-route differences with CIs and marks `no demonstrated difference` when the CI includes 0. Seed fixed in code (constant) and recorded in output.
- Acceptance: unit test: identical inputs give diff 0 and CI [0,0]; a constant +0.1 shift gives CI tightly around 0.1; same seed gives identical output on two runs; `eval compare` prints a table for the fixture results.
- Tests: deterministic seed tests; paired-ness test (shuffling pairs changes the CI).
- Out of scope: report file (T04).
- Owner: agent

### S4-T04 - Report generator
- Why: `eval/report.md` is a deliverable.
- Depends on: T03
- Touches: `cmd/eval/report.go`, `eval/report.md` (generated)
- Details: `eval report` reads all `eval/results/*/*.json` and writes `eval/report.md`: ablation ladder table (overall + per route, metric, CI vs A0), router/system/call-graph/indexing sections when their JSON exists (skipped sections say `not yet measured`), a header listing commit, tag, models, query-file hashes. Deterministic output (sorted, no timestamps except the recorded run dates).
- Acceptance: running `eval report` twice yields byte-identical `eval/report.md`; with fixture results it contains one row per config and the CI columns; sections with missing data print `not yet measured`.
- Tests: golden report for fixture results.
- Out of scope: README text.
- Owner: agent

### S4-T05 - GitHub mining tool (fetch)
- Why: semantic queries come from real issues, limiting author bias ([#sources](../eval-protocol.md#sources)).
- Depends on: none
- Touches: `internal/config/config.go`, `.env.example`, `cmd/eval/mine.go`
- Details: add `GITHUB_TOKEN` to config and `.env.example` (empty, commented, never committed; `.gitignore` already ignores `.env`). `eval mine --since <tag date minus 12 months> --until <tag date>` uses GitHub REST (`/search/issues?q=repo:kubernetes/kubernetes is:pr is:merged merged:A..B` paginated, then per PR: body for `Fixes|Closes|Resolves #N`, the issue (`title`, body first paragraph), changed files via `/pulls/{n}/files`). Without `GITHUB_TOKEN` it exits with `set GITHUB_TOKEN (read-only, public repos) in .env`. Handles rate limits (`X-RateLimit-Remaining`, sleep until reset), caches raw responses under `eval/candidates/cache/` (gitignored) so reruns are free. Writes `eval/candidates/semantic.raw.jsonl`.
- Acceptance: unset token -> exact message above and non-zero exit; with token, `eval mine --limit 50` writes 50 rows each with `pr`, `issue`, `title`, `first_paragraph`, `files[]`; second run makes 0 HTTP calls (cache); unit test with `httptest.Server` covers pagination and rate-limit sleep.
- Tests: `httptest.Server` for pagination, rate limit, missing "Fixes" line.
- Out of scope: filtering (T06).
- Owner: agent

### S4-T06 - Mining filters, leakage check, tiers
- Why: apply the eligibility and leakage rules mechanically before human review.
- Depends on: T05
- Touches: `cmd/eval/mine_filter.go`
- Details: `eval mine-filter` reads raw rows and keeps PRs where: changed non-test non-generated files (use the walk exclusion predicates) number 1-5, all in the slice, all present in `files` at the pinned tag. Leakage: drop if the issue title/first paragraph contains any changed file basename (without `.go`) or any function/type name declared in the changed files (word-boundary, case-sensitive). Output tiered candidates `eval/candidates/semantic.tier1.jsonl` (1-5 files, 12 months), `tier2` (window +6 months), `tier3` (6-8 files; flag for manual per-file review), with the drop reasons counted. Report yield per tier.
- Acceptance: prints yield per tier and drop-reason counts; unit tests show a PR whose issue text names `schedulePod` is dropped and one with 6 files appears only in tier3; yield of tier1 + tier2 >= 25 or the shortfall message points to tier3 and KEP questions (max 5).
- Tests: table tests for each filter rule and leakage.
- Out of scope: choosing queries (human, T08/T10/T11).
- Owner: agent

### S4-T07 - Structural and lexical samplers
- Why: seeded, stratified sampling removes author bias ([#sources](../eval-protocol.md#sources)).
- Depends on: T01
- Touches: `cmd/eval/sample.go`
- Details: `eval sample --kind structural --seed 101` picks 14 in-slice symbols with >= 1 `gopackages` caller, stratified: >= 4 whose GT callers include `interface_method` or `func_value` edges (>= 1 for DEV, >= 3 for TEST, split assignment 4 DEV / 10 TEST by seeded shuffle respecting the strata), the rest random; query text `who calls <Name>` or `what does <Name> call` (seeded coin). Draft labels: files of GT callers/callees (`how: gt-graph`, grade 2 for direct, applying D-012 for > 15 files). `--kind lexical --seed 202` picks 11 in-slice symbols (~7 exact identifiers `where is <Name> defined`, ~4 split fragments like `pod backoff` generated from `SplitIdentifiers(name)` first two parts); draft label = the file defining the symbol (`how: grep`) plus files with other symbols named identically (grade 1). Both write candidates under `eval/candidates/` with the seed and strata in `_meta`. Never uses retrievers.
- Acceptance: same seed gives byte-identical output on two runs; structural output contains >= 4 stratum-qualifying symbols (script check `jq`); every draft label path passes `eval validate`; `grep -rn "retrieve\." cmd/eval/sample.go` returns nothing.
- Tests: determinism test; strata test with a fixture graph.
- Out of scope: human review (T09, T12, T13).
- Owner: agent

### S4-T08 - [human] Label DEV semantic (8 queries)
- Why: DEV set for all tuning.
- Depends on: T06
- Touches: `eval/queries.dev.jsonl`
- Details: pick 8 from tier1 (<= 2 KEP questions may be written instead; write KEP questions BEFORE reading code). For each: read the PR, check leakage by eye, grade changed files 2/1 (incidental = 1 with note), record `how`. Never run the retrievers while labeling.
- Acceptance: `eval validate` passes for these rows; 8 semantic rows in DEV; each label has `how`; no row was produced using `indexer search` (self-attest in Results).
- Tests: none.
- Out of scope: other routes.
- Owner: human

### S4-T09 - [human] Label DEV structural + lexical (7 queries)
- Why: complete DEV (4 structural, 3 lexical).
- Depends on: T07
- Touches: `eval/queries.dev.jsonl`
- Details: review the sampler's draft labels: confirm 3 DEV lexical with grep, and 4 structural by spot-checking callers in the source (gopls `callHierarchy` or grep); correct grades; record `how`.
- Acceptance: `eval validate` shows DEV = 3/4/8 by route (lexical/structural/semantic); every structural DEV query has labels with `how` set.
- Tests: none.
- Out of scope: TEST labeling.
- Owner: human

### S4-T10 - [human] Label TEST semantic, part 1 (9 queries)
- Why: TEST semantic = 17.
- Depends on: T08
- Touches: `eval/queries.test.jsonl`
- Details: as T08, 9 queries; the tier-2 pool and KEP questions (total KEP across DEV+TEST <= 5).
- Acceptance: `eval validate` clean for these rows; KEP count tracked in Results.
- Tests: none.
- Out of scope: running any retriever on TEST queries.
- Owner: human

### S4-T11 - [human] Label TEST semantic, part 2 (8 queries)
- Why: finishes TEST semantic.
- Depends on: T10
- Touches: `eval/queries.test.jsonl`
- Details: as T10; for tier3 (6-8 files) PRs review each changed file and record the grade-1 reason in `note`.
- Acceptance: TEST semantic = 17; every grade-1 label on a 6-8 file PR has a `note`.
- Tests: none.
- Out of scope: freezing.
- Owner: human

### S4-T12 - [human] Label TEST structural + lexical, part 1 (9 queries)
- Why: TEST has 10 structural + 8 lexical = 18.
- Depends on: T09
- Touches: `eval/queries.test.jsonl`
- Details: 5 structural + 4 lexical from the sampler; review as T09.
- Acceptance: `eval validate` clean; strata counts so far recorded.
- Tests: none.
- Out of scope: freezing.
- Owner: human

### S4-T13 - [human] Label TEST structural + lexical, part 2 (9 queries)
- Why: finish TEST.
- Depends on: T12
- Touches: `eval/queries.test.jsonl`
- Details: remaining 5 structural + 4 lexical.
- Acceptance: `eval validate --strict` exits 0: DEV 3/4/8 and TEST 8/10/17; >= 4 structural queries meet the interface/func-value stratum.
- Tests: none.
- Out of scope: tuning.
- Owner: human

### S4-T14 - Freeze TEST
- Why: no tuning before the TEST hash is recorded (D-010).
- Depends on: T13, T02
- Touches: `cmd/eval/freeze.go`, `docs/plan/decisions.md` (D-010)
- Details: `eval freeze` runs `validate --strict`, prints `sha256(eval/queries.test.jsonl)` and writes it into D-010 with date and commit; `eval freeze --verify` recomputes and compares. Commit both files and the decisions entry in one commit. Add a CI step `go run ./cmd/eval freeze --verify` (skips with a message if D-010 is still PENDING).
- Acceptance: `go run ./cmd/eval freeze` prints the hash and D-010 contains it; editing one byte of the test file makes `freeze --verify` exit non-zero; CI verifies.
- Tests: unit test for hash and verify.
- Out of scope: running TEST.
- Owner: agent

### S4-T15 - B0 keyword baseline (BM25)
- Why: a plain keyword baseline shows what embeddings add (D-030).
- Depends on: S3-T10, S4-T02
- Touches: `internal/retrieve/bm25.go`, `internal/evalconfig/`
- Details: `BM25` implements `Retriever`. Build an in-memory inverted index at startup over the in-slice files at the pinned tag (read from `KUBE_DIR`, same exclusion rules as walk): each file is one document tokenized with `chunk.SplitIdentifiers` (originals and parts, lowercased). Score = standard BM25 with fixed `k1=1.2`, `b=0.75` (constants, not tuned), IDF `ln(1 + (N - n + 0.5)/(n + 0.5))`; the query is tokenized with the same splitter, duplicate query terms counted once. Results go through the shared `CollapseToFiles` (slice filter before the cut, D-039). Register config `B0`. No database, no model.
- Acceptance: `go test -count=1 ./internal/retrieve/...` passes with a hand-computed BM25 score on a 3-document fixture (tolerance 1e-9); `go run ./cmd/eval run --config B0 --split dev` writes `eval/results/B0/dev.json`; `grep -rn "k1\|0.75" internal/retrieve/bm25.go` shows constants only, no flags or config.
- Tests: unit tests for tokenization parity with `SplitIdentifiers`, IDF, length normalization (a long file with one match scores below a short file with one match), empty query.
- Out of scope: tuning k1/b, stemming, using any database.
- Owner: agent

### S4-T16 - Run A0, A1, B0 on DEV; ANN overlap on DEV
- Why: first honest numbers; ANN gate on real queries ([#ann-overlap](../eval-protocol.md#ann-overlap)).
- Depends on: S4-T14, S4-T03, S4-T04, S4-T15
- Touches: `eval/results/A0/dev.json`, `eval/results/A1/dev.json`, `eval/results/B0/dev.json`, `eval/report.md`
- Details: `ann-overlap --queries eval/queries.dev.jsonl` (query text only) for qwen3 on `chunk_raw` and `chunk_header`; if mean < 0.95 tune `ef_search` first (record in Results). Then `eval run --config A0 --split dev`, `A1`, `B0`, `eval compare --base A0 --cand A1 --split dev`, `eval compare --base B0 --cand A0 --split dev`, `eval report`. A0, A1 and B0 have no tuned parameters, so this does not violate the freeze rule.
- Acceptance: the three JSON files exist with 15 per-query rows each; `eval/report.md` shows A0, A1, B0 with CI columns; overlap numbers are in Results.
- Tests: none.
- Out of scope: TEST run.
- Owner: agent

## Measurements to record
- Query-set counts by route/split, KEP count, tier used per semantic query.
- A0/A1/B0 DEV retrieval metrics with CIs ([#retrieval-metrics](../eval-protocol.md#retrieval-metrics)): `eval/results/{A0,A1,B0}/dev.json`, `eval/report.md`.
- ANN overlap@10 on DEV (mean, min) per variant; chosen `ef_search`.
- TEST sha256 (decisions.md D-010).

## Exit criteria / Definition of Done
`eval validate --strict` passes; D-010 contains the TEST sha256 and CI verifies it; A0/A1/B0 DEV results and report committed; no TEST run performed; ROADMAP updated.

## Risks + mitigations
- Mining yield < 25 semantic queries: tiers and KEP fallback (max 5) are built in; if still short, stop and ask the author (do not lower the leakage rules).
- Labeling is the longest human block and cannot be automated: keep it going in parallel with the summary run; never lower the labeling rules, and apply the Phase 1 priority list in ROADMAP.md if the sprint slips.
- GitHub API limits: unauthenticated runs are blocked deliberately; caching makes reruns free.
- Accidental label leakage from the system under test: T08-T13 self-attestation and the sampler's `grep retrieve.` check.

## Demo check
`go run ./cmd/eval validate --strict && go run ./cmd/eval freeze --verify && go run ./cmd/eval compare --base A0 --cand A1 --split dev` prints the A0/A1 table with CIs; `eval compare --base B0 --cand A0` shows BM25 versus embeddings.

## Results
(empty)
