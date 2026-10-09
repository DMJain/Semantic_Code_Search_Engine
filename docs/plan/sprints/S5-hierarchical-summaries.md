# S5 - Hierarchical summaries

## Goal
Finish the summary hierarchy (file and package levels), embed all three summary levels, and build the coarse-to-fine semantic retriever (A2), tuned on DEV.

## Entry criteria
S4 done (harness, DEV labels, TEST frozen). Function-level summary run finished (`make summarize-status`: 0 pending, failed < 2%); if still running, finish it first (S5-T02).

## Links
[architecture.md#stage-summarize](../architecture.md#stage-summarize), [#stage-embed](../architecture.md#stage-embed), [#vector-indexes](../architecture.md#vector-indexes), [#semantic-retriever](../architecture.md#semantic-retriever), [#result-contract](../architecture.md#result-contract), [#ollama-requests](../architecture.md#ollama-requests); [eval-protocol.md#configurations](../eval-protocol.md#configurations), [#statistics](../eval-protocol.md#statistics). Decisions: D-014, D-020, D-039. Run procedure: [S1 overnight procedure](S1-corpus-symbols.md#overnight-run-procedure).

## Tasks

### S5-T01 - Migration 00006: packages
- Why: package summaries need a target ID (summary uniqueness already exists since 00003).
- Depends on: none
- Touches: `internal/store/migrations/00006_packages.sql`, `internal/walk/`, `cmd/indexer/walk.go`
- Details: `CREATE TABLE packages (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, import_path text NOT NULL UNIQUE, in_slice boolean NOT NULL DEFAULT false); INSERT INTO packages (import_path, in_slice) SELECT package, bool_or(in_slice) FROM files GROUP BY package;` Down drops the table. `walk` keeps `packages` in sync inside its transaction.
- Acceptance: `select count(*) from packages where in_slice` > 0; rerunning `walk` leaves counts unchanged; `make migrate-down migrate-up` exits 0 and `\d packages` is unchanged.
- Tests: migration integration test; walk test asserts packages sync.
- Out of scope: summaries.
- Owner: agent

### S5-T02 - Finish function level and audit
- Why: file summaries depend on complete function summaries.
- Depends on: none
- Touches: this file (Results)
- Details: resume the run if needed; `summarize --level function --retry-failed` once; record counts by status and reasons (`truncated` count). Read 30 random function summaries (seed 3; a second look after the S1-T14 gate, now on the full run) for obvious failures (empty, code dump, off-topic); if > 10% bad, stop and revise `fn-v1` -> `fn-v2` (new `prompt_version`, rerun is a new decision to be flagged to the author, since it costs another overnight run).
- Acceptance: `select status,count(*) from summaries where level='function' group by 1` shows 0 pending; failed < 2% of eligible; Results has counts and the 30-sample verdicts.
- Tests: none.
- Out of scope: new prompts unless the 10% threshold trips.
- Owner: agent

### S5-T03 - File-level summarizer
- Why: middle level of the hierarchy.
- Depends on: T01, T02
- Touches: `internal/summarize/file.go`, `cmd/indexer/summarize.go` (`--level file`)
- Details: `prompt_version = "file-v1"`. Input = file path, package, top-level type/func signatures with doc first line, and the function summaries of the file (in source order); user message: "Describe this file's responsibility in 2-3 sentences: what it is for, key types, how it fits in its package." If input > 3000 estimated tokens, map-reduce: summarize groups of <= 30 function summaries to partials, then summarize the partials (partials not stored). `input_hash = sha256(prompt_version | path | signatures | child summary hashes)` so a changed function summary invalidates its file summary. Target = `files.id`. Same pause/resume, status and truncation behavior as functions ([#ollama-requests](../architecture.md#ollama-requests)). Files with no done function summaries but a doc/type content still get a summary from declarations; files with nothing to say are skipped (count printed).
- Acceptance: `indexer summarize --level file --limit 10` makes 10 `done` rows with `level='file'`; rerun makes 0 calls; changing one function summary's `input_hash` in the DB and rerunning regenerates exactly that file's summary (integration test).
- Tests: prompt golden; map-reduce split test with a fixture of 100 summaries; httptest-based truncation test; guarded integration test for hash invalidation.
- Out of scope: package level.
- Owner: agent

### S5-T04 - Package-level summarizer
- Why: top level of the hierarchy.
- Depends on: T03
- Touches: `internal/summarize/pkg.go`, `cmd/indexer/summarize.go` (`--level package`)
- Details: `prompt_version = "pkg-v1"`. Input = package import path and file summaries; "Describe the package's purpose and its main components in 3-4 sentences." Large packages (more than 40 files) use two-level map-reduce grouped by file-path directory then by 15-file batches. Target = `packages.id`. Hash includes child file summary hashes. Same resumability.
- Acceptance: `indexer summarize --level package` finishes with one `done` row per in-slice package with at least one file summary; rerun makes 0 calls; the largest package (by files) completes without `truncated` failures.
- Tests: golden prompt; map-reduce test; truncation test.
- Out of scope: embedding.
- Owner: agent

### S5-T05 - Run file and package summaries (operational)
- Why: produces the data for coarse-to-fine.
- Depends on: T04
- Touches: this file (Results)
- Details: `make summarize-run` variant per level: `caffeinate -i go run ./cmd/indexer summarize --level file` then `--level package`, run sequentially with the same pause/resume rules. Record wall time and rows per hour per level and compare to the S1 benchmark estimate (`eval/results/bench/summarize.json`).
- Acceptance: `select level,status,count(*) from summaries group by 1,2` shows 0 pending and failed < 2% for all three levels; measured vs benchmark throughput recorded.
- Tests: none.
- Out of scope: re-benchmarking.
- Owner: agent

### S5-T06 - Embed summaries
- Why: coarse-to-fine retrieval searches summary vectors.
- Depends on: T05
- Touches: `cmd/indexer/embed.go`
- Details: add target kinds `summary_function`, `summary_file`, `summary_package` to `embed --kinds`. Text = summary text; title = `<file path> <qualified symbol name>` for functions, file path for files, package import path for packages (per [#stage-embed](../architecture.md#stage-embed)). `input_hash` = summary `input_hash`; `embeddings.target_id` = `summaries.id`. Default model, slice only. Build partial HNSW indexes per `(model, kind)` with `indexer vindex` for the three kinds (small kinds like packages may skip the index; the command logs `seq scan is cheaper` for tables under 1000 rows and skips).
- Acceptance: `select target_kind,count(*) from embeddings where model='qwen3-embedding:0.6b' group by 1` shows counts equal to the `done` summary rows per level minus the `embed_failures` rows of that kind; rerun embeds 0; `EXPLAIN` uses the HNSW index for `summary_function`.
- Tests: extend the embed unit tests for new kinds and title composition.
- Out of scope: other models (S8).
- Owner: agent

### S5-T07 - Coarse-to-fine retriever (A2)
- Why: the core contribution ([#semantic-retriever](../architecture.md#semantic-retriever)).
- Depends on: T06
- Touches: `internal/retrieve/coarse.go`, `cmd/indexer/search.go`, `internal/evalconfig/`
- Details: `Coarse{Weights{Direct, Package, Func}}.Retrieve`: embed query once; top-k_p = 5 package summaries, top-k_f = 30 file summaries, top-k_fn = 50 function summaries and top-k_c = 50 `chunk_header` chunks (each a nearest-neighbor query on its own `(model, kind)` index, see [#vector-indexes](../architecture.md#vector-indexes)). Per file: `score = Wd*max(file_sim, best_chunk_sim) + Wp*sim(pkg of file) + Wf*max(function_sim in file)` with missing components 0; `Evidence` lists the contributing hits with levels. Weights from config, default `0.5/0.2/0.3`. Candidate files = union of all hit files, ranked by score, collapse via the shared helper (slice filter before dedupe and cut, D-039), limit 10.
- Acceptance: unit test with fixture similarities verifies the exact weighted score and ranking; `indexer search --config A2 "<query>"` returns files with evidence from at least two levels for some query; wiring: `evalconfig` registers `A2`.
- Tests: unit tests for score combination (each component alone, all missing), integration test (guarded) on fixture vectors.
- Out of scope: tuning (T08).
- Owner: agent

### S5-T08 - Tune weights on DEV and run A2
- Why: A2 vs A1 is the headline hierarchical-summary result; D-020 limits tuning to DEV, coarse grid.
- Depends on: T07
- Touches: `cmd/eval/tune.go`, `eval/results/A2/dev.json`, `eval/results/tuning/s5-weights.json`, `docs/plan/decisions.md` (D-020)
- Details: `eval tune --config A2 --grid weights` evaluates at most 12 weight triples (Direct in {0.4,0.6}, Package in {0,0.15,0.3}, Func = 1 - Direct - Package, clamp >= 0 and drop invalid), DEV only (command refuses `--split test`). Every grid row logged to `eval/results/tuning/s5-weights.json` (weights + metrics). Select by DEV nDCG@10, ties by Recall@10. Record the chosen weights in D-020. Run `eval run --config A2 --split dev`, `eval compare --base A0 --cand A2`, `--cand A2 --base A1`, `eval report`. Report that DEV tuning is optimistic.
- Acceptance: tuning JSON has <= 12 rows; D-020 contains weights and the DEV nDCG; `eval/results/A2/dev.json` exists; `eval/report.md` includes A2 with CIs vs A0 and A1; `eval tune --split test` exits non-zero.
- Tests: unit test for grid generation (count <= 12, weights sum to 1); refusal test.
- Out of scope: tuning anything else; TEST run.
- Owner: agent

## Measurements to record
- Summary rows per level and status, failed reasons, throughput per level vs benchmark ([eval-protocol.md#indexing-metrics](../eval-protocol.md#indexing-metrics)) in Results.
- Vector counts per `(model, kind)`; DB size.
- A2 DEV metrics with CIs vs A0 and A1; the full tuning grid: `eval/results/A2/dev.json`, `eval/results/tuning/s5-weights.json`, `eval/report.md`.

## Exit criteria / Definition of Done
All three summary levels complete and embedded; A2 on DEV reported honestly even if it does not beat A1; weights recorded in D-020; ROADMAP updated.

## Risks + mitigations
- File/package prompts overflow context: map-reduce and truncation guard.
- A2 does not beat A1: report as is; investigate by ablating components (Direct only vs +Package vs +Func) in Results, within the 12-combination cap.
- Another overnight run needed (prompt revision): flag to the author; cut M1 first if schedule slips (ROADMAP).

## Demo check
`indexer search --config A2 "why does the scheduler retry unschedulable pods"` prints files with evidence by level; `eval compare` shows A0/A1/A2 on DEV.

## Results
(empty)
