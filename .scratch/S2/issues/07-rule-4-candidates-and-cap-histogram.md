# 07: Rule 4 candidates and the cap histogram

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 48-51, 53-55, 98, 100, 97, 99.

**What to build:** The candidate set for an interface call is computed correctly and the cap value is chosen from evidence. Candidates are concrete indexed types that declare every method name of the interface (value or pointer receivers, interfaces excluded, signatures not compared). Same-package embedded interfaces are expanded recursively with a cycle guard; a cross-package or unresolvable embedded interface contributes only the names known (a superset) and increments `interfaces_partial`; an interface with no known methods yields no candidates. Candidates are counted over the full indexed set before any slice filtering. The candidate function takes N as a parameter.

`indexer graph --stats --uncapped-histogram` runs the same code path as a real run with a writer that writes nothing and prints p50, p90, p99, max and the counts at 5, 10, 20, 50 and 100 candidates; the output is committed as a result file. N is the smallest of 5, 10, 20, 50, 100 such that at least 95% of interface call sites have at most N candidates, and is recorded in D-075 with the histogram file name. If that N is above 50, stop: set this ticket's Status to ready-for-human and write the escalation. The production path in this ticket still stores no Rule 4 rows (they stay `deferred_interface`). Bump the resolver version.

**Blocked by:** 06

**Status:** done

- [x] Fixture tests: `interface{Get;Set}` with `A{Get,Set}` and `B{Get}` gives only A; same-package embedded interface expands; cross-package embedded gives `interfaces_partial=1`; an embedding cycle terminates; value and pointer receivers both match; an interface with zero known methods gives no candidates
- [x] The dry run leaves the edge count unchanged; its total equals the stored row count of an uncapped fixture run
- [x] The histogram result file is committed for the real tree
- [x] D-075 records N (chosen by the stated rule, not from precision or recall) and the histogram file name, replacing PENDING; if N > 50 the ticket is ready-for-human and nothing else is required
- [x] Production `graph` still stores no Rule 4 rows and counts them `deferred_interface`; resolver version bumped
- [x] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [x] One commit `S2-07: ...` that also sets this ticket's Status to done
- [x] Report: the commands run (trimmed output) and the commit hash

Out of scope: Storing Rule 4 rows or capped rows (ticket 08).

## Report

**What changed.** Rule 4's candidate set is computed and the cap N chosen from
evidence; production still stores no Rule 4 rows (`deferred_interface`). New:
interface facts in the names-only pass (`FileFacts.Interfaces`, `SourceFacts`),
`NameIndex.AddInterfaces` + `InterfaceMethodNames` (recursive same-package
expansion with a cycle guard, `partial` for a cross-package/unresolvable embed),
`Index.InterfaceCandidates(names, pkg, name, n, mode)` over the whole indexed
set, `Rule4` handling in `Analyze`, `CapHistogram`/`CapHistogramResult`, the
`--uncapped-histogram` flag and `eval/results/callgraph/cap-histogram.json`.
Resolver version 4 -> 5.

**Algorithm.** Known method names = direct `method_elem` names + same-package
embedded interfaces recursively (cycle guard). A cross-package/unknown embed
contributes no names and sets partial (candidates become a superset); zero known
names -> no candidates. Candidates = concrete types (value/pointer receiver,
interfaces excluded, signatures not compared) whose symbol-index method-name set
covers every known name; counted over the full indexed set. `n` is a parameter.

**Fixtures.** `Store{Get;Set}` + `A{Get(value),Set(pointer)}` + `B{Get}` -> only
A; `Both{Base(Read);Write}` expands the same-package embed (C, D);
`UsesExt{ext.Extra;Write}` -> candidates C, D with `interfaces_partial=1`; cycle
terminates; empty interface -> no candidates. Uncapped package stats:
`ambiguous: 6`, `interface_method: 6`, `interfaces_partial: 1`,
`deferred_interface: 0`, invariant `call sites 6 = stored 6 + skipped 0 +
deferred 0 [ok]`. Production: no rows, `deferred_interface: 4`. Dry run leaves
the fixture edge count unchanged and its candidate total equals the uncapped
run's `interface_method` rows.

**Real-tree histogram** (`go run ./cmd/indexer graph --stats --uncapped-histogram`,
6633 files): 9547 interface call sites, 55555 candidates; p50 2, p90 10, p99 78,
max 407; at most 5: 7957, 10: 8624, 20: 9010, 50: 9381, 100: 9477;
`interfaces_partial: 762`. 95% = 9070 sites, so **N = 50** (50 covers 9381,
98.3%; 20 covers 9010, 94.4%). Recorded in D-075 with the histogram file name.
No escalation. The dev DB edge count was unchanged by the dry run (26917 before
and after) and no `interface_method` row was stored.

**Ambiguity.** `interfaces_partial` is counted per interface call site (the
counter the rule increments), not per distinct interface. A cross-package or
unresolvable embed contributes no names, so such an interface's candidate set is
a superset of the true one. An interface call site with zero candidates (540 on
the real tree: `interface{}` and interfaces whose only methods come from a
cross-package embed) stores no row and stays `deferred_interface`, the residual
the cap ticket removes. The `CappedCandidates` mode and the `candidates_capped`
branch in `Analyze` are deterministic and tested, but the production path never
takes them here (all callers pass N = 0); the cap ticket wires them. The
histogram dry run forces every indexed file (it is
evidence about the whole tree) but uses the same extraction/resolution path and
writes nothing, so it leaves `edges` and the `graphed_*` markers untouched.

**Review fixes.** (1) The N > 50 escalation gate is implemented:
`CapHistogram.Result` sets `Escalate` when the chosen N is 0 or above 50
(`maxNonEscalatingCap`), covered by `TestCapHistogramEscalatesAbove50` (only 100
clears 95% -> `ChooseN == 100`, `Escalate == true`; the CLI stops, Status stays
`ready-for-human`). This does not change the committed N = 50. (2)
`InterfaceCandidates` now sorts before applying the cap, so a capped result is
deterministic; `TestInterfaceCandidatesCapped` (out-of-order symbols, exact-fit
cap not capped, one over capped) and `TestAnalyzeRule4Capped` exercise the
`candidates_capped` vocabulary (one unresolved row with no callee) and the
uncapped N-row path. (3) D-075 is amended (2026-10-14) to state that
`interfaces_partial` is counted per interface call site, not per distinct
interface, matching the code, the glossary and the 762 figure. (4) The
`--uncapped-histogram` run now labels its output honestly ("uncapped histogram
dry run: ... stored-row total (Rule 4 candidates included), nothing written")
instead of a misleading edge estimate. (5) The `InterfacesPartial` field doc and
the stray comment above `type TypeRef` are fixed.

**Checks.** `make test` green (throwaway DB per test, no new skips);
`make ci-local` green. No git command was run; the commit hash is left to the
orchestrator's post-review commit.

**Commands (trimmed).**
```
$ go run ./cmd/indexer graph --stats --uncapped-histogram
6633 files to graph
uncapped histogram dry run: 6633 files, 277614 stored-row total (Rule 4 candidates included), nothing written
...
cap histogram: 9547 interface call sites, 55555 candidates total
  p50: 2, p90: 10, p99: 78, max: 407
  at most 5: 7957, 10: 8624, 20: 9010, 50: 9381, 100: 9477
  interfaces_partial: 762
  chosen N: 50
$ make test      # green
$ make ci-local  # green
```
