# 11: `eval callgraph` precision and recall report

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 79-88, 95, 98, 97, 99.

**What to build:** `go run ./cmd/eval callgraph` compares the tree-sitter graph with the ground truth on the slice and records honest numbers by call kind. The scored universe is caller and callee both in slice; identity is the `(caller, callee)` symbol pair. Every stored tree-sitter edge from an in-slice caller falls in exactly one bucket (scored in slice, resolved out of slice, unresolved by reason) and the report asserts the buckets sum to the total, with a pointer to `graph --stats` for the skip counters. The GT side has its own partition that sums to its total. Resolved out-of-slice edges are neither true nor false positives; their share is printed. An ambiguous call site is a true positive only if the GT callee is among its candidates; recall counts a GT pair as found if any tree-sitter edge (including an ambiguous candidate) covers it. Precision by tree-sitter kind, recall by GT kind (with an `unknown` bucket for tree-sitter edges of unknown kind); ambiguous rate and mean candidate-set size per kind. Same-line duplicates collapse identically on both sides. The report refuses to run while `graph --status` shows any stale file. Output is JSON (`precision`, `recall`, `by_kind` with `precision`, `recall`, `ambiguous_rate` per kind, `counts`) and a markdown table.

**Blocked by:** 09, 10

**Status:** done

- [ ] Tests over hand-built edge rows: perfect, all-ambiguous, missing, out-of-slice share, unresolved by reason; both partitions sum to the total; a multi-line call and a same-line duplicate score identically on both sides; refusal when a file is stale; golden for the JSON and the markdown table
- [ ] `go run ./cmd/eval callgraph` exits 0 on the real data, writes the JSON with the fields above, prints the table
- [ ] The stdout and JSON state that the numbers are in-slice only and give the out-of-slice share
- [ ] Headline numbers, edge counts by source, resolution and kind, and wall times are copied into the S2 Results section of the sprint doc
- [ ] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [ ] One commit `S2-11: ...` that also sets this ticket's Status to done
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: The general evaluation harness (S4); error analysis (ticket 12); line-level comparison as a primary metric.

## Report

**What changed**
- `cmd/eval/callgraph.go` — new `callgraph` subcommand (registered in
  `cmd/eval/main.go`). Pure `score(scoreInput)` core over hand-built rows plus the
  DB wiring (`staleFileCount`, `loadScoreInput`, `loadGTPartition`), JSON writer
  and markdown renderer.
- `cmd/callgraph-gt/store.go`, `main.go` — the store action now writes
  `eval/results/callgraph/gt-store.json` (`-store-out`, default path) with the GT
  partition and kind counts; the report reads and asserts it. `gtStoreResult` and
  `writeGTStoreResult` are unit-tested.
- `internal/graph/freshness.go` — extracted the shared staleness predicate
  (`StaleReasons`, `Reason*`) and the symbol/name-index loaders
  (`LoadSymbols`, `BuildNameIndex`) from `cmd/indexer/graph.go`, so the report
  refuses on exactly the files `graph --status` lists. `cmd/indexer` now delegates
  to them.
- Docs: sprint Results section; committed `eval/results/callgraph/result.json`
  and `gt-store.json`.

**Metric implementation (D-074)**
- Identity `(caller_id, caller_raw?, callee_id)`; line only collapses same-line
  duplicates. TS bucket partition over stored TS edges with an in-slice caller:
  `scored_in_slice` / `resolved_out_of_slice` / `unresolved(reason)`, counted
  independently in pass 1 and categorised in pass 2, asserted to sum.
- Precision = TP / in-slice-candidate call-site units; a unit is TP iff any
  candidate is a GT in-slice pair. Out-of-slice-resolved and unresolved units are
  excluded (share printed).
- Recall = found / distinct in-slice GT pairs; a pair is found if any TS unit of
  that caller names the callee (ambiguous candidates included).
- Precision by TS kind, recall by GT kind (a pair attributed to its
  lexicographically lowest kind so by-kind universes sum), plus `ambiguous_rate`
  and mean candidate size per kind; an `unknown` bucket for TS units. A rate with
  a zero denominator is undefined and marshals as JSON `null` (`n/a` in the
  table); `ambiguous_rate` and mean candidate size are over all units of the
  kind, precision over scored units, recall over GT pairs (documented in `note`).
- GT partition read from the store file and asserted to sum and to match stored
  rows.

**Test coverage** (cmd/eval/callgraph_test.go, cmd/callgraph-gt/store_test.go):
perfect, all-ambiguous (TP and FP), missing (recall < 1), out-of-slice share,
unresolved by every reason, GT partition sum/rows/by-kind assertions, same-line
duplicate vs multi-line scoring identically (and units 1 vs 2), an ambiguous unit
straddling the slice boundary, `by_kind` summing to the overall universes,
undefined rates as JSON null, JSON and markdown goldens, staleness refusal at the
helper and at `reportCallgraph` (throwaway DB), GT store result round-trip, and an
end-to-end throwaway-DB run.

**Real headline numbers** (in-slice only): precision **0.8613** (10056/11676),
recall **0.8388** (8906/10618). By kind: func 0.9977/0.9248, concrete_method
0.9987/0.9412, interface_method 0.7610/0.6843 (ambiguous rate 0.4734, mean 2.23),
func_value n/a (undefined)/0.0026, unknown 0.3614/n/a (undefined). Out-of-slice share 0.1066; TS
buckets 26946/6289/25766 (sum 59001); GT partition 15254 = 12780 + 1990 + 484
(rows 12586).

**Commands run**

```
set -a; source .env; set +a
go run ./cmd/indexer graph --status          # 0 files stale
GOMEMLIMIT=14GiB go run ./cmd/callgraph-gt   # 21.52 s, wrote gt-store.json
go run ./cmd/eval callgraph                  # 6.77 s, wrote result.json
make test                                     # green, throwaway DB per test, no skips
make ci-local                                 # green (vet, tests, golangci-lint 0, shellcheck, actionlint)
```

**Metric ambiguity**: the spec fixes edge-level buckets but scores call-site units
for precision and distinct pairs for recall, so the out-of-slice share is edge-
level (6289/59001) while a separate precision-excluded share is unit-level
(0.7215). Both are printed. A rate with a zero denominator is undefined, not 0:
it marshals as JSON `null` and prints as `n/a` (`func_value.precision`,
`unknown.recall` on the real data), with the zero denominator visible in the
counts block. `ambiguous_rate` and mean candidate size are over all units of the
kind (documented in `note`), so zero-candidate units dilute the mean.

**Commit**: not made here (the orchestrator commits after review, per the run
instructions).
