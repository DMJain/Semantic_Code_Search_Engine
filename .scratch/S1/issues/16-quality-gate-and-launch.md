# 16: [human] Quality gate and launch

Plan: S1-T14 (second half) (`docs/plan/sprints/S1-corpus-symbols.md`). Spec user stories: 36, 37, 38, 55, 56, 57, 58, 8, 69.

**What to build:** Before the long run, the agent summarizes a stratified seeded sample of 200 functions (seed 3) and reports the `too_large` and `truncated` counts separately. 30 of its done rows (seed 3) are printed with their code, and the author grades each good or bad against the written rubric (D-068), recording the first failing criterion. At most 3 bad passes. A failed gate bumps the prompt version and repeats; a second failure stops for the owner; a failed version's function-level rows are deleted after the new gate passes and before any embedding. After a passing gate the author starts `make summarize-run` in their own terminal; the agent records the start, the expected end and the count remaining, and takes two status readings. The sprint's sign-off checklist states that CI is checked only by `make ci-local`.

**Blocked by:** 15 (Run tooling and `verify-slice`)

**Status:** ready-for-human

- [ ] Verdicts are recorded as one JSON line per summary (qualified name, input hash, verdict, first failing criterion) in the S1 scratch folder, with at most 3 bad and the author's approval in Results
- [ ] `select distinct prompt_version from summaries where level='function'` returns only the current version
- [ ] Two `make summarize-status` readings from the author-started run show `done` strictly increasing
- [ ] The keep-awake assertion is visible while the run is active
- [ ] Sign-off checklist in Results states that CI is checked only by `make ci-local` (nothing is pushed, D-069)
- [ ] One commit `S1-16: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: File and package levels; supervising the full run to completion.
