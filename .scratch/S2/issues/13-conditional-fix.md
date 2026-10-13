# 13: Conditional fix from error analysis

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 91, 92, 97, 99.

**What to build:** Created only if ticket 12's fix rule triggers (one cause over 20% of errors, change at most 150 lines in one area). Applies the single fix with a golden test, bumps the resolver version, re-runs the graph and the report, and keeps the before and after reports. If the rule did not trigger, this ticket is deleted by ticket 12.

**Blocked by:** 12 (only if triggered)

**Status:** wontfix (fix rule did not trigger)

- [ ] The fix touches one area and is at most 150 lines; a golden test covers it
- [ ] Before and after `eval callgraph` reports are both kept in Results
- [ ] Resolver version bumped; the graph rerun leaves 0 stale files
- [ ] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [ ] One commit `S2-13: ...` that also sets this ticket's Status to done
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Any second fix; anything not named by ticket 12.

## Disposition (ticket 12, 2026-10-14)

The fix rule did not trigger, so no fix is attempted. The sampled cause table
(`eval/results/callgraph/error-sample.json`, 60 samples) is `unknown_receiver`
30 (50.0%), `dynamic` 25 (41.7%), `closure` 5 (8.3%). Two causes exceed 20%, but
neither has a bounded <= 150-line single-area fix: `dynamic` is the deliberate
design (D-073, D-075) and `unknown_receiver` needs return-type propagation
through chained calls and field/local type inference, new machinery well over 150
lines. `closure` is below the threshold. This ticket is therefore not worked and
is set `wontfix`; the file is kept for the orchestrator rather than deleted.
