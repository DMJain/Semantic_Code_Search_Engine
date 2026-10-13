# 09: Function values and the final whole-repo run

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 44, 59, 62, 75-78, 98, 97, 99.

**What to build:** The tree-sitter graph is complete. A call through a function-typed local, parameter or closure variable gets kind `func_value` and is stored `unresolved` with reason `func_value`; a package-level function variable (known from the declared-name index) is reclassified from `no_match` to `func_value`. `deferred_local` is removed, so no deferred code path remains: from here a fresh graph cannot contain deferred rows. Bump the resolver version.

Then the final whole-repo run: every indexed file graphed, wall time recorded, `--stats` by resolution, reason and kind recorded, `--status` shows 0 stale files. One fixture-run test proves that all five unresolved reasons occur.

**Blocked by:** 08

**Status:** done

- [x] Fixture goldens: local func variable, func-typed parameter, closure variable, package-level func variable (func_value, not no_match)
- [x] No `deferred_*` counter exists in `--stats` or the code; the invariant (total = stored + skipped) passes
- [x] A test over a fixture run asserts every allowed `unresolved_reason` value occurs at least once
- [x] Final whole-repo run: wall time, `--stats` output by resolution, reason and kind recorded; `graph --status` prints 0 stale files; `graph` then prints `0 files to graph`
- [x] Resolver version bumped
- [x] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [x] One commit `S2-09: ...` that also sets this ticket's Status to done
- [x] Report: the commands run (trimmed output) and the commit hash

Out of scope: Ground truth, the report, error analysis.

## Report

Function values. A bare identifier call whose operand is a local name (`:=`,
`var`, parameter or closure variable) is now stored `unresolved` with reason
`func_value` and kind `func_value` (no callee), replacing `deferred_local`. A
package-level function variable is recorded by the declared-name index as kind
`func_value` and reclassified from `no_match` to `func_value`, for both
same-package and `alias.Name` calls. The declared-name index marks a
package-level `var`/`const` as `func_value` in all three shapes: a written
function type (`var f func()`), an inferred function-literal initialiser
(`var f = func() {}`), and a written type naming a package-level function type or
alias (`type Callback func(); var c Callback` or `type Callback = func(); var c Callback`).
A package-level declared function stays
Rule 1 `exact` (`TestSourceFactsFuncVariables`, `TestAnalyzeFunctionValues`,
`TestGraphStageFuncValues`). `deferred_local` and `Stats.Deferred` are removed;
the invariant is now `call sites = stored + skipped`, asserted in `Stats.Check`
and printed. Resolver version bumped 6 -> 7.

Fixture cases added after review. `funcvalues` gains `var InferredFunc = func() {}`,
`type Callback func(); var NamedFunc Callback`, and `depone.Hook()` (an exported
function variable in `depone` called through the import alias); the golden test
pins all seven func_value callers plus `PackageFunc()` staying `exact`.
`TestSourceFactsFuncVariables` checks the declared-name kinds directly.

Raw-call cap. The first whole-repo attempt failed in
`test/e2e/storage/podlogs/podlogs.go`: an immediately-invoked function literal
gave a ~7.4 KB `callee_raw`, overflowing the `edges_uniq` btree index (limit
~2.7 KB). `callShape` now truncates the raw text on a rune boundary to
`maxCallRaw` = 1000 bytes. Added `TestExtractCapsLongCallRaw` and
`TestGraphStageLongCallRawFitsIndex`. This is required for the final run and is
the smallest fix at the storage boundary; flagged as an incidental but necessary
change.

Five reasons. `TestGraphStageAllUnresolvedReasonsOccur` runs the whole fixture
with `Rule4N 3` and asserts the stored `unresolved_reason` set is exactly
`external`, `no_match`, `unknown_receiver`, `func_value`, `candidates_capped`.

Final whole-repo run (dev DB, 6633 files, graphed markers cleared):
`set -a; source .env; set +a; time go run ./cmd/indexer graph --stats`:

```
6633 files to graph
graphed 6633 files, stored 262420 edges
edges stored: 262420
  resolution exact: 75778
  resolution name_only: 4802
  resolution ambiguous: 62248
  resolution unresolved: 119592
  reason external: 45340
  reason no_match: 4570
  reason unknown_receiver: 65711
  reason func_value: 3804
  reason candidates_capped: 167
call kind func: 57649
call kind concrete_method: 18129
call kind interface_method: 36499
call kind func_value: 3804
call kind unknown: 146339
percent ambiguous: 23.7%
unresolved: 119592
skipped_builtin: 33369
skipped_conversion: 1164
interfaces_partial: 685
invariant: call sites 296953 = stored 262420 + skipped 34533 [ok]
```

Wall time 54.8 s (go run, includes the build). The declared-name extension moved
20 real-tree package-level function variables from `no_match` to `func_value`
versus the first run (totals unchanged). No `deferred_*` counter printed.
`graph --status` then printed `0 files stale`; a second `graph` printed
`0 files to graph` / `graphed 0 files, stored 0 edges`; the stored
`unresolved_reason` values are exactly the five allowed
(`candidates_capped`, `external`, `func_value`, `no_match`, `unknown_receiver`).

Checks. `CGO_ENABLED=1 go test -count=1 ./internal/graph/... ./cmd/indexer/...
./internal/ci/...` green; `make test` green (throwaway DB per test, no new skips);
`make ci-local` green. Fixture counts updated: 70 -> 83 stored rows, 15 -> 16
fixture files. No commit made (orchestrator commits).
