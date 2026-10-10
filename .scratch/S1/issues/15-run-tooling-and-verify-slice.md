# 15: Run tooling and `verify-slice`

Plan: S1-T14 (first half) (`docs/plan/sprints/S1-corpus-symbols.md`). Spec user stories: 35, 59, 61, 60.

**What to build:** The author gets the commands for the long run: `make summarize-run` (build, keep-awake wrapper, concurrency 1), `make summarize-pause` (interrupt, finishing the in-flight item), `make summarize-status` (counts by status for the current prompt version only, with an estimate from the last 100 completions), and `make verify-slice` (recomputes the slice hash and fails on drift). The overnight-run procedure in the sprint file is the single source for the steps.

**Blocked by:** 11 (`indexer summarize` with resume and sampling), 14 (Freeze the slice)

**Status:** ready-for-agent

- [ ] `make verify-slice` exits 0, and exits non-zero after a temporary edit of the slice definition's drop list (reverted afterwards)
- [ ] Status counts only the current prompt version: a row of another version is not counted (test)
- [ ] Pause and resume checked by counts on a small run: wait until `done >= start + 5`, interrupt, assert no partial rows, resume, `done` increases and there are 0 duplicate `(level, target, prompt version)` rows
- [ ] The keep-awake assertion is visible while a run is active
- [ ] One commit `S1-15: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Starting the full run; file and package levels.
