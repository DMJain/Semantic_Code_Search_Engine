# 10: Ground truth stored with source isolation

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 14-24, 35, 82, 100, 36, 97, 99.

**What to build:** The VTA call graph from ticket 01 is stored as ground-truth edges. Only edges whose caller and callee are both in slice files are kept. Each SSA function maps to a symbol's qualified name with pointer receivers preserved; closures attribute to the enclosing declared function; callers with no symbol (package variable initialisers, synthetic `init`, bound-method and thunk wrappers) are dropped and counted; generic instantiations map to their origin symbol; any in-slice edge that cannot be mapped to a symbol id is counted as `gt_unmapped` and printed. Kinds come from the SSA call mode (static function `func`, static method `concrete_method`, invoke `interface_method`, dynamic `func_value`). Rows have `source='gopackages'`, `resolution='exact'`, no reason, the callee's qualified name as raw text and the call line (0 if unavailable). All gopackages rows are replaced in one transaction; two runs give identical counts. Printed counts include a GT-side partition (mapped, dropped by reason, `gt_unmapped`) that sums to the total.

Isolation (D-077): `graph --stats` and `graph --status` ignore GT rows. The structural-retriever criterion is added to S6-T05 and recorded in D-077 (already written; verify it is present).

**Blocked by:** 01, 02

**Status:** done

- [ ] Committed tiny module fixture (interface, function value, concrete method, closure, a generic): `go run ./cmd/callgraph-gt` over it gives rows of all four kinds, correct closure attribution, dropped synthetic callers counted, `gt_unmapped` printed, and identical counts on a second run
- [ ] The GT-side partition printed by the run sums to the total (asserted)
- [ ] With one GT row and one tree-sitter row for the same caller/callee pair, `graph --stats` and `graph --status` output is unchanged by the GT row (test)
- [ ] Real run: `select call_kind, count(*) from edges where source='gopackages' group by 1` shows all four kinds; wall time and peak RSS recorded; `--check` still prints `file-set: equal`
- [ ] The S6-T05 acceptance and D-077 carry the one-edge isolation criterion; S6-T05 also states that the retriever follows only edges with a callee and lists unresolved rows separately with their reason (verify present)
- [ ] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [ ] One commit `S2-10: ...` that also sets this ticket's Status to done
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: The report; any retriever; using GT in the server (forbidden).

## Report

**What changed.** `cmd/callgraph-gt` gained a store action (the default; `--check`
and `--measure` still work). New `cmd/callgraph-gt/store.go` loads the slice,
maps each in-slice VTA edge to a `symbols.qualified_name` and replaces every
`source='gopackages'` row in one transaction. `cmd/indexer/graph.go` needed no
change: no non-eval path reads `edges` (the only read is the tree-sitter delete).
The fixture `testdata/module/pkg/scheduler/scheduler.go` now carries an
interface, a concrete method, a function value, a closure, a generic function, a
generic pointer-receiver method, a plain pointer-receiver method and a
package-variable initialiser.

**Review fixes.** (MAJOR-1) `recvName` now renders the receiver exactly as
`internal/parse` does, preserving type parameters (`(*Heap[T])`), so generic
methods map instead of being filed `gt_unmapped`; the fixture carries a generic
pointer-receiver method and a plain pointer-receiver method, both asserted to
map. (MAJOR-2) `buildSliceGraph` takes a `rootPolicy`: `--measure` uses
`rootsSliceOnly` (slice functions only, the original minimal input, story 10) and
the store uses `rootsSliceAndSynthetic` (plus position-less synthetic slice
functions, story 17); `--measure` again reproduces `vta_edges` 39616 and
`in_slice_edges` 14469. (Minor) `gtStore.check` now compares an independently
counted total against the buckets, so a categorisation bug fails it; `mapped` is
labelled pre-dedup and the collapse count is printed; deduplication sorts edges
into a total order before collapsing, so `ByKind` is deterministic; `gt_unmapped`
counts only in-slice edges (both endpoints in slice files); `syntheticSliceFn`
has a nil guard.

**Mapping and kinds.** A function maps to `<pkg>.<Name>` and a method to
`<pkg>.(<Recv>).<Name>` (receiver rendered from the SSA receiver type with
pointer-ness and type parameters preserved). Closures walk `Parent()` to the
enclosing declared function; generic instantiations resolve through `Origin()`;
the package is `walk.ImportPath` of the in-slice file, so it matches the symbols
table. Synthetic callers (`Origin()==nil && Parent()==nil && Synthetic!=""`) are
dropped: `init` (package initializer), `bound_method`, `thunk`, `wrapper`. A
caller or callee that maps to no symbol id counts as `gt_unmapped`. Kinds come
from the SSA call mode: `IsInvoke()` -> `interface_method`; else static callee
with a receiver -> `concrete_method`; static callee without one -> `func`; else
`func_value`.

**Fixture result.** `gt edges: total 15 = mapped 13 (pre-dedup) + dropped 2 (init
1, wrapper 1) + gt_unmapped 0`; kinds `func 8, concrete_method 3,
interface_method 1, func_value 1`; 13 rows; identical on a second run. `Unmapped`
is asserted 0, the generic method `(*Heap[T]).AddOrUpdate` and the plain pointer
receiver `(*counter).Inc` are asserted to map, and the partition sums.

**Real run.** `GOMEMLIMIT=14GiB /usr/bin/time -l go run ./cmd/callgraph-gt`
(dev DB): wall 23.91 s real (includes the `go run` build), peak RSS
5,482,627,072 bytes (5.11 GiB). Partition total 15254 = mapped 12780 (pre-dedup) +
dropped 1990 (init 777, bound_method 327, wrapper 886) + gt_unmapped 484. Kinds:
concrete_method 4559, func 4389, interface_method 3115, func_value 523; 12586 rows
(194 duplicate collapses). `callee_raw LIKE '%[%'` is 121 (was 0), so generic
receivers now map; `gt_unmapped` fell 705 -> 484. `select call_kind, count(*) ...
group by 1` shows all four kinds; `--check` still prints `file-set: equal (876
files)`.

**Measure vs store.** `--measure` uses `rootsSliceOnly` and reproduces the
committed `eval/results/callgraph/gt-memory.json`: `vta_edges` 39616,
`in_slice_edges` 14469, packages 332. Its `ssa_functions` field (263605
committed) is a per-run sample of `ssautil.AllFunctions`, which is not
deterministic across runs (re-runs give ~263.4k); it was already a single sample
in the committed evidence and is unaffected by the root policy, so the file is
not re-recorded. The store's extra roots are the position-less synthetic slice
functions; they add the dropped package-variable-initialiser callers.

**Isolation.** `TestGraphStatsStatusIgnoreGroundTruth` (cmd/indexer) inserts one
`gopackages` row duplicating a real tree-sitter caller/callee pair and proves
`graph --stats` and `graph --status` output are unchanged. S6-T05 and D-077
already carry the one-edge criterion; S6-T05 also already states the retriever
follows only edges with a callee and lists unresolved rows separately with their
reason.

**Checks.** `CGO_ENABLED=1 go test -count=1 ./cmd/callgraph-gt/...
./cmd/indexer/... ./internal/ci/...` passes; `make test` green (throwaway DB per
test, no skips); `make ci-local` green (go vet, tests, golangci-lint 0 issues,
shellcheck, actionlint).

**Ambiguity.** Drop reasons are the four buckets above; `init` also catches any
position-less `init`-named synthetic. Out-of-slice edges are not "considered"
(the total is the in-slice partition); `gt_unmapped` is the 484 in-slice edges
whose caller or callee had no symbol, from generic/`#n`/duplicate
qualified-name mismatches (not dependency callees).
