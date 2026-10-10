# 11: Summarization throughput benchmark (S1-T11)

Plan: S1-T11 (`docs/plan/sprints/S1-corpus-symbols.md`)

**What to build:** `indexer summarize --bench` samples 200 eligible functions (seed 7) with the production prompt at concurrency 1 then 2, times 20 stratified file-level and 3 package-level draft prompts, and writes `eval/results/bench/summarize.json` without inserting `summaries` rows.

**Blocked by:** 10

**Status:** ready-for-agent

**Spec user stories:** 29, 30, 28

- [ ] JSON has `fn_per_hour_c1`, `fn_per_hour_c2`, `file_per_hour`, `pkg_per_hour`, `skipped_fns`, `slice_fns_eligible`, `file_strata` (at least 3 strata)
- [ ] `select count(*) from summaries` is unchanged by the command
- [ ] Sampler determinism and stratification test (shared with T10)
- [ ] One commit `S1-T11: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Tuning the prompt beyond one sanity read of 10 outputs.
