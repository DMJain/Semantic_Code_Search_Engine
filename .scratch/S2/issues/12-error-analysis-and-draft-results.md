# 12: Error analysis and draft Results

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 89-96, 100, 97, 99.

**What to build:** Explain where tree-sitter loses. A seeded (seed 11) sample of 30 false positives and 30 false negatives, stratified by kind, is classified by cause: unknown receiver, embedded struct, generic, closure, build tag, dynamic, and any other cause found. Recall is reported per stratum (interface method, function value, plain, external). The fix rule is applied: a fix is attempted only if one cause accounts for more than 20% of errors and the change is at most 150 lines in one area; a triggered fix becomes ticket 13 with before and after reports. Unfixed limitations are recorded honestly. The sprint Results section is drafted (numbers cited from tickets 01 to 11, no new decisions). The owner adds the sign-off line afterwards, as in S1, and the ROADMAP S2 row is flipped to done after that.

**Blocked by:** 11

**Status:** done

- [x] The cause table sums to 60 samples; recall per stratum is reported
- [x] The fix-rule verdict is stated (triggered or not, with the cause percentages); if triggered, ticket 13 is created and Status is set accordingly
- [x] Results in the sprint doc are drafted; `grep -n PENDING docs/plan/decisions.md` shows no S2-related PENDING and the open-questions file has no Q-04 or Q-05 unless ticket 01 left Q-05 open with a reason
- [x] A line is left for the owner's sign-off (not filled by the agent)
- [x] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [ ] One commit `S2-12: ...` that also sets this ticket's Status to done
- [x] Report: the commands run (trimmed output) and the commit hash

Out of scope: Any fix (ticket 13); the sign-off itself; the ROADMAP flip.

## Report

Sampling tool. `eval callgraph --sample` (flag on the existing `callgraph`
subcommand) reuses the report's scoring core: `score()` builds the FP/FN lists as
unexported fields and `enumerateErrors` returns them, so the sample cannot drift
from the report. FPs are scored tree-sitter units with no matching GT pair; FNs
are in-slice GT pairs no unit covers. 30+30, seed 11, stratified by kind, quotas
proportional (largest remainder), drawn through `internal/seeded`. Output:
`eval/results/callgraph/error-sample.json` (seed 11; fp_quota func 0,
concrete_method 0, interface_method 10, unknown 20; fn_quota func 5,
concrete_method 4, interface_method 14, func_value 7), 60 rendered items with
caller, callee/raw, line and source.

Cause table (sums to 60): `unknown_receiver` 30 (50.0%), `dynamic` 25 (41.7%),
`closure` 5 (8.3%), `embedded_struct` 0, `generic` 0, `build_tag` 0, `other` 0.
Classified by reading each sample's caller source line.

Recall per stratum (in-slice GT): plain 0.9333 (7134/7644), interface method
0.6843 (1771/2588), function value 0.0026 (1/386), external undefined (out of
the scoring universe; 7474 unresolved-`external` + 6289 resolved-out-of-slice
in-slice-caller call sites).

Fix-rule verdict: not triggered. Two causes exceed 20% but neither has a bounded
<= 150-line single-area fix (`dynamic` is the deliberate D-073/D-075 design;
`unknown_receiver` needs return-type and field/local type inference). `closure`
is 8.3%. Ticket 13 is not triggered; `13-conditional-fix.md` is set `wontfix`
with this verdict.

Results draft: added `### Error analysis and draft Results (S2 ticket 12)` and a
blank `### S2 close-out sign-off` to `docs/plan/sprints/S2-call-graph.md`. No new
decisions; `grep -n PENDING docs/plan/decisions.md` shows no S2-related PENDING;
Q-05 stays open with ticket 01's reason (allowed).

Commands run (trimmed):

```
$ TEST_DATABASE_URL=$DATABASE_URL REQUIRE_TEST_DB=1 go test ./cmd/eval/...   # ok
$ set -a; source .env; set +a; go run ./cmd/eval callgraph --sample
error sample: 30 fp (of 1620) + 30 fn (of 1712), seed 11 -> eval/results/callgraph/error-sample.json
$ make test        # green, no skips
$ make ci-local    # green
```

Commit hash: the orchestrator commits after review; not run by the agent.
