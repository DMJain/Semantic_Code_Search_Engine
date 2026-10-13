# S9 - 3B vs 7B summaries

## Goal
Regenerate the summaries with a 3B coder model, evaluate A2 with them (DEV, then one TEST run), and report Recall@10 next to peak memory during summarization. The result decides Phase 2's heavy-mode default model (D-036).

## Entry criteria
S8 done: every Phase 1 configuration has its single TEST result; D-028 filled; the A2 (7B) TEST result exists. Ollama running. Disk and DB space for a second summary set.

## Links
[architecture.md#stage-summarize](../architecture.md#stage-summarize), [#stage-embed](../architecture.md#stage-embed), [#semantic-retriever](../architecture.md#semantic-retriever), [#ollama-requests](../architecture.md#ollama-requests); [eval-protocol.md#configurations](../eval-protocol.md#configurations), [#freeze](../eval-protocol.md#freeze), [#statistics](../eval-protocol.md#statistics); decisions D-005, D-020, D-036, D-037. Run procedure: [S1 overnight procedure](S1-corpus-symbols.md#overnight-run-procedure) (pause/resume, `caffeinate -i`).

## Tasks

### S9-T01 - Migration 00008: summaries keyed by model
- Why: two summary sets (7B and 3B) must coexist without overwriting each other.
- Depends on: none
- Touches: `internal/store/migrations/00008_summaries_model_key.sql`, `internal/summarize/`, `internal/retrieve/coarse.go`
- Details: replace the unique index `summaries_uniq (level, target_id, prompt_version)` with `(level, target_id, prompt_version, model)`. The summarizer's skip logic and the coarse retriever take a `SUMMARY_MODEL` setting (default `qwen2.5-coder:7b`) and read only rows of that model; embeddings of summaries are keyed by the summary row id, so the two sets never mix. Existing 7B rows are untouched.
- Acceptance: after `make migrate`, `\d summaries` shows the new unique index; `eval run --config A2 --split dev` with `SUMMARY_MODEL` unset reproduces the stored S5 DEV numbers exactly (diff of `eval/results/A2/dev.json` per-query rows is empty apart from the date).
- Tests: integration test inserting the same `(level, target, prompt_version)` for two models succeeds; unit test that the retriever filters by model.
- Out of scope: running anything with the 3B model.
- Owner: agent

### S9-T02 - 3B summary configuration and memory sampler
- Why: the comparison needs the same prompts with a different model, and peak memory measured the same way as in Phase 2 ([eval-protocol.md#memory-measurement](../eval-protocol.md#memory-measurement)).
- Depends on: S9-T01
- Touches: `.env.example`, `internal/config/config.go`, `scripts/measure-mem.sh`, `Makefile`
- Details: `SUMMARY_MODEL=qwen2.5-coder:3b` (the one name from S9-T01 is also used for the Ollama request) with its own `num_ctx` entry; the prompts (`fn-v1`, `file-v1`, `pkg-v1`) are unchanged. `scripts/measure-mem.sh [--match <pattern>] [--interval 1] <outfile> [-- command...]` (macOS): every second it sums the physical footprint (`footprint -p`) of every process whose name matches the pattern (default `ollama`: the server and its `runner` children), writes CSV `timestamp,bytes,processes`, and prints the peak and the final value on exit; with `-- command` it runs the command and samples its process tree. `make summarize-run-3b` sets the model and starts the sampler beside the run. P2-S2-T06 reuses this script and adds the Linux variant.
- Acceptance: `ollama pull qwen2.5-coder:3b` is done; `make summarize-run-3b` with `--limit 5` produces 5 `done` rows with `model = 'qwen2.5-coder:3b'` and a CSV with at least 3 samples; the script prints the peak footprint and the number of processes summed (at least 2 while a runner process exists); `shellcheck scripts/measure-mem.sh` passes.
- Tests: shellcheck; unit test for the config override.
- Out of scope: the full run.
- Owner: agent

### S9-T03 - 7B footprint baseline
- Why: D-036 needs the 7B peak memory measured the same way as the 3B (no earlier run was sampled).
- Depends on: S9-T02
- Touches: `cmd/indexer/summarize.go` (`--bench-level function|file|package`), `eval/results/s9/footprint-7b-{function,file,package}.csv`, this file (Results)
- Details: for each level run `indexer summarize --bench --bench-level <level>` with `SUMMARY_MODEL=qwen2.5-coder:7b`, the same `num_ctx` and concurrency as the later 3B runs, under `scripts/measure-mem.sh --match ollama`. Bench runs write no `summaries` rows. Record the peaks.
- Acceptance: the three CSV files exist; Results lists the three peak values, `num_ctx` and concurrency; `select count(*) from summaries` is unchanged by the task.
- Tests: none.
- Out of scope: 3B runs (T04, T05).
- Owner: agent

### S9-T04 - [human] Run 3B function summaries
- Why: the critical compute step of this sprint.
- Depends on: S9-T02
- Touches: this file (Results), `eval/results/s9/footprint-function.csv`
- Details: run `make summarize-run-3b` (function level, same skip rule, `caffeinate -i`, pausable per the S1 procedure) with the sampler running. When done run `--retry-failed` once. Record functions per hour, failed rows with reasons, peak footprint.
- Acceptance: `select status,count(*) from summaries where level='function' and model='qwen2.5-coder:3b' group by 1` shows 0 pending and failed under 2% of eligible; the footprint CSV exists with its peak noted in Results.
- Tests: none.
- Out of scope: tuning the prompt.
- Owner: human

### S9-T05 - [human] Run 3B file and package summaries
- Why: A2 needs all three levels.
- Depends on: S9-T04
- Touches: this file (Results), `eval/results/s9/footprint-file-pkg.csv`
- Details: run the file level, then the package level with the 3B model and the sampler. Record throughput per level and peak footprint.
- Acceptance: file and package rows for the 3B model show 0 pending and failed under 2% of eligible; the CSV exists.
- Tests: none.
- Out of scope: re-running the 7B levels.
- Owner: human

### S9-T06 - Embed the 3B summaries and register A2-3B
- Why: the retriever searches summary vectors.
- Depends on: S9-T05
- Touches: `internal/evalconfig/`, this file (Results)
- Details: `indexer embed --kinds summary_function,summary_file,summary_package` with `SUMMARY_MODEL=qwen2.5-coder:3b` and the default embedding model; build the HNSW indexes for the new rows if the planner needs them. Register config `A2-3B` = A2 with `SUMMARY_MODEL=qwen2.5-coder:3b` and the SAME weights as A2 (D-020; no retuning, so the comparison isolates the summary model).
- Acceptance: embedding counts for the 3B set equal its `done` summary rows per level; `indexer search --config A2-3B "<query>"` returns 10 files; `eval run --config A2-3B --split dev` writes `eval/results/A2-3B/dev.json`.
- Tests: unit test that `A2-3B` and `A2` differ only in `SUMMARY_MODEL`.
- Out of scope: retuning weights.
- Owner: agent

### S9-T07 - A2-3B on DEV and single TEST run
- Why: the comparison result ([#freeze](../eval-protocol.md#freeze), D-037).
- Depends on: S9-T06
- Touches: `eval/results/A2-3B/{dev,test}.json`, `eval/report.md`
- Details: run `eval compare --base A2 --cand A2-3B --split dev`; then `eval freeze --verify` and `eval run --config A2-3B --split test --final` exactly once; then `eval compare --base A2 --cand A2-3B --split test` and the same for A1 as base. Report Recall@10 with paired-bootstrap CIs.
- Acceptance: `A2-3B/test.json` has 35 per-query rows and the same `query_file_sha256` as the other TEST results; `eval run ... --final` a second time is refused by the harness.
- Tests: none.
- Out of scope: any change after seeing TEST.
- Owner: agent

### S9-T08 - Decide the heavy-mode default model and record memory
- Why: apply D-036 and publish the memory-versus-quality table.
- Depends on: S9-T07, S9-T03
- Touches: `docs/plan/decisions.md` (D-036), `eval/report.md`, `eval/results/s9/summary.json`
- Details: apply the D-036 rule to the TEST Recall@10 difference (7B minus 3B; `eval compare --base A2 --cand A2-3B` prints 3B minus 7B, so the decision function negates it). Write `eval/results/s9/summary.json` with, per model: Recall@10 (overall, with CI), nDCG@10, functions per hour, peak physical footprint per summarization level. Record the decision with the numbers and the date in D-036 and add a sentence to the report.
- Acceptance: D-036 contains the model choice, the CI of the difference and peak footprints; `summary.json` has both models; `eval report` is deterministic and includes the table.
- Tests: unit test for the decision function on three synthetic CIs (excludes 0 favoring 7B, includes 0, excludes 0 favoring 3B).
- Out of scope: Phase 2 implementation.
- Owner: agent

### S9-T09 - README: 3B vs 7B result
- Why: the README written in S8 predates this result; the portfolio must show the outcome and its memory cost.
- Depends on: S9-T08
- Touches: `README.md`, `scripts/check-readme-numbers.sh`
- Details: add to the Evaluation section a table of A2 vs A2-3B Recall@10 with CIs and peak physical footprint per model and level, plus the D-036 decision, with numbers from `eval/results/s9/summary.json`; extend the numbers script to check them.
- Acceptance: `scripts/check-readme-numbers.sh` exits 0; `grep -c "A2-3B" README.md` is at least 1.
- Tests: the script.
- Out of scope: Phase 2 README.
- Owner: agent

## Measurements to record
- A2-3B DEV and TEST retrieval metrics with CIs vs A2 and A1: `eval/results/A2-3B/{dev,test}.json`, `eval/report.md`.
- Functions per hour per level and peak physical footprint per level for the 3B model, and the 7B footprint measured the same way (S9-T03): `eval/results/s9/`.
- Decision D-036 (decisions.md).

## Exit criteria
A2-3B has one DEV and one TEST result; D-036 is decided from TEST per its rule; the memory table is in `eval/report.md` and the README; ROADMAP updated.

## Risks + mitigations
- Another long compute run: pausable procedure, same as S1; if it cannot finish, S9 is the last Phase 1 item to drop and Phase 2's default falls back to 3B with the gap documented.
- 3B quality collapse on file/package prompts: report as is; the rule decides.
- Mixed summary sets in one query: the model filter test (S9-T01) prevents it.

## Demo check
`go run ./cmd/eval compare --base A2 --cand A2-3B --split test` prints the table; `cat eval/results/s9/summary.json` shows Recall@10 next to peak footprint for both models.

## Results
(empty)
