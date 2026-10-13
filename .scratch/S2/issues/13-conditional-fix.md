# 13: Conditional fix from error analysis

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 91, 92, 97, 99.

**What to build:** Created only if ticket 12's fix rule triggers (one cause over 20% of errors, change at most 150 lines in one area). Applies the single fix with a golden test, bumps the resolver version, re-runs the graph and the report, and keeps the before and after reports. If the rule did not trigger, this ticket is deleted by ticket 12.

**Blocked by:** 12 (only if triggered)

**Status:** ready-for-agent (conditional)

- [ ] The fix touches one area and is at most 150 lines; a golden test covers it
- [ ] Before and after `eval callgraph` reports are both kept in Results
- [ ] Resolver version bumped; the graph rerun leaves 0 stale files
- [ ] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [ ] One commit `S2-13: ...` that also sets this ticket's Status to done
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Any second fix; anything not named by ticket 12.
