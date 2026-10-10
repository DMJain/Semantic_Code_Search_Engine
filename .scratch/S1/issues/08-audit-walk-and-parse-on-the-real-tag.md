# 08: Audit walk and parse on the real tag

Plan: S1-T08 (first half) (`docs/plan/sprints/S1-corpus-symbols.md`). Spec user stories: 22, 34.

**What to build:** The walk and the parse are run on the pinned tag and the results are measured instead of assumed: counts for files, slice files, LOC, symbols by kind, in-slice functions and functions with a body of at least three lines, plus the parse-error count. A seeded random sample of 30 stored symbols is re-read from the source lines and checked for name, receiver and doc comment.

**Blocked by:** 06 (Struct and interface symbols), 07 (Raw calls per function)

**Status:** ready-for-agent

- [ ] Results hold the counts and the 30-sample table (seed 42) with 0 unexplained mismatches; mismatches are explained or fixed
- [ ] Parse-error count recorded
- [ ] Each recorded command, the tag and an output hash are in Results
- [ ] One commit `S1-08: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Fixes beyond the parse bugs this audit finds.
