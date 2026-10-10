# 12: Summarization throughput benchmark

Plan: S1-T11 (`docs/plan/sprints/S1-corpus-symbols.md`). Spec user stories: 45, 46, 44.

**What to build:** `indexer summarize --bench` measures summarization on 200 eligible functions (seed 7) with the production prompt at concurrency 1 then 2, and times 20 stratified file-level and 3 package-level draft prompts, writing the measurements to a results file without inserting any summary row.

**Blocked by:** 11 (`indexer summarize` with resume and sampling)

**Status:** ready-for-agent

- [ ] The results file has `fn_per_hour_c1`, `fn_per_hour_c2`, `file_per_hour`, `pkg_per_hour`, `skipped_fns`, `slice_fns_eligible` and `file_strata` with at least 3 strata
- [ ] The summaries table has the same row count before and after the command
- [ ] Sampler determinism and stratification test shared with ticket 11
- [ ] One commit `S1-12: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Tuning the prompt beyond one sanity read of 10 outputs.
