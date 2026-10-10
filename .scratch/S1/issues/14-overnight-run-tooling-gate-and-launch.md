# 14: Overnight-run tooling, quality gate and launch (S1-T14)

Plan: S1-T14 (`docs/plan/sprints/S1-corpus-symbols.md`)

**What to build:** Add `make summarize-run` (build, `caffeinate -i`, concurrency 1), `summarize-pause`, `summarize-status` (current prompt version only), `verify-slice`. Run the quality gate: stratified seeded sample of 200, seeded 30 graded by the author against the rubric, at most 3 bad passes; a failed gate bumps `PromptVersion` and redoes it; a second failure stops for the owner. The agent never starts the full run; the author does. The agent part of this ticket ends at the gate verdicts.

**Blocked by:** 13, 10

**Status:** ready-for-agent

**Spec user stories:** 36, 37, 38, 39, 40, 35, 42

- [ ] `make verify-slice` exits 0, and non-zero after a temporary edit of the `slice.go` drop list
- [ ] Verdicts in `.scratch/S1/quality-gate-<prompt_version>.jsonl` with at most 3 bad and `approved` from the author; `too_large`/`truncated` counts among the 200 reported separately
- [ ] `select distinct prompt_version from summaries where level='function'` returns only the current version
- [ ] Owner-started run: two `make summarize-status` readings with `done` strictly increasing; pause and resume by counts (done >= start + 5, SIGINT, no partial rows, resume, done increases, 0 duplicates); the caffeinate assertion is visible
- [ ] Sign-off checklist in Results lists the draft-PR push decision and the unobserved CI risk; one commit `S1-T14: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: File and package levels; supervising the full run to completion.
