# S2 - Call graph

## Goal
Build the tree-sitter call graph with resolution confidence, build the VTA ground-truth graph for the slice, and measure tree-sitter edge precision/recall by call kind.

## Entry criteria
S1 done: symbols populated, slice final (D-005), summary run launched (it keeps running in the background; pause it for heavy jobs per [S1 overnight procedure](S1-corpus-symbols.md#overnight-run-procedure)). S2 does no summarization.

## Links
[architecture.md#stage-graph](../architecture.md#stage-graph), [#stage-callgraph-gt](../architecture.md#stage-callgraph-gt), [#data-model](../architecture.md#data-model); [eval-protocol.md#call-graph-metrics](../eval-protocol.md#call-graph-metrics). Decisions: D-011, D-019, D-023, D-025.

## Tasks

### S2-T01 - Migration 00004: edge columns
- Why: precision/recall by call kind and evidence lines need them.
- Depends on: none
- Touches: `internal/store/migrations/00004_edges_columns.sql`
- Details: `ALTER TABLE edges ADD COLUMN call_line integer, ADD COLUMN call_kind text CHECK (call_kind IN ('func','concrete_method','interface_method','func_value','unknown'));` `ALTER TABLE files ADD COLUMN graphed_sha text;` `CREATE UNIQUE INDEX edges_uniq ON edges (source, caller_id, callee_raw, call_line, coalesce(callee_id, 0));` Down reverses.
- Acceptance: `make migrate` ok; `\d edges` shows both columns and the index; down/up works.
- Tests: extend the migration integration test with duplicate-edge insert rejected.
- Out of scope: populating.
- Owner: agent

### S2-T02 - Call-expression extraction with local type info
- Why: resolution needs the receiver's type; raw names alone are not enough (D-019).
- Depends on: T01
- Touches: `internal/graph/extract.go`
- Details: re-parse a file with tree-sitter and produce per function `[]CallSite{Raw, Qualifier, Name, Line, Kind, ReceiverType}`. Build per file: import alias map (`alias -> import path`, dot and blank imports ignored); per function a local type environment: receiver (`s *Scheduler`), parameters, `var x T`, `x := T{...}`/`&T{...}`, `x := NewT(...)` where `NewT` is a same-package function whose single declared return type is known, struct field types of known same-package structs (`s.cache` -> field type). Unknown types stay unknown (never guessed). `Kind`: qualifier is an import alias -> `func`; receiver type known concrete -> `concrete_method`; known interface type -> `interface_method`; call on a local identifier typed as func -> `func_value`; else `unknown`.
- Acceptance: `go test -count=1 ./internal/graph/...` golden tests pass for a fixture covering each Kind.
- Tests: golden JSON of call sites for fixture files (method chains, shadowed variables, closures, embedded structs left unknown).
- Out of scope: resolution to symbol IDs (T03).
- Owner: agent

### S2-T03 - Resolution
- Why: converts call sites to edges with honest confidence.
- Depends on: T02
- Touches: `internal/graph/resolve.go`
- Details: input call sites + a symbol index (`qualified_name`, `name`, `receiver`, package). Order: (1) same-package function by name -> exact; (2) `alias.Name` via import map to a symbol in that package -> exact; (3) method with known concrete receiver type -> `(*T).Name` or `(T).Name` -> exact; (4) known interface receiver type -> candidates = all methods named `Name` whose receiver type satisfies the method name set (name-only match on method names declared in the interface); one edge per candidate, `ambiguous` (D-023); (5) unknown receiver -> all symbols with that method name in the same package -> `name_only` if exactly one, else `ambiguous`. No match (zero candidates): store one edge with `callee_id NULL`, `callee_raw` set, resolution `name_only`. External (non-indexed) callees also produce NULL-callee edges so recall accounting is complete.
- Acceptance: golden tests: same-package call -> exact; aliased import -> exact; concrete method -> exact; interface call -> N ambiguous rows with identical `callee_raw`; unresolved external -> NULL callee row.
- Tests: golden resolution fixtures, one per rule, including a name collision across two packages.
- Out of scope: DB writes (T04).
- Owner: agent

### S2-T04 - `indexer graph` command
- Why: stage wiring, idempotent and resumable.
- Depends on: T03
- Touches: `cmd/indexer/graph.go`, `cmd/indexer/main.go` (add `graph`)
- Details: files where `graphed_sha IS DISTINCT FROM sha` (all indexed files, not only slice, so the structural retriever can serve the whole repo). Per file transaction: delete edges (`source='treesitter'`) of its symbols, insert new ones, set `graphed_sha`. Load the symbol index once into memory. Flags `--limit`, `--stats`. SIGINT-safe. Print edges by resolution, by `call_kind`, % ambiguous, NULL-callee count.
- Acceptance: `indexer graph` then again prints `0 files to graph`; INT-and-rerun yields the same `select count(*) from edges where source='treesitter'` as an uninterrupted run; `select resolution,count(*) from edges group by 1` shows all three values.
- Tests: guarded integration idempotency/resume test on a fixture tree.
- Out of scope: ground truth.
- Owner: agent

### S2-T05 - callgraph-gt: offline loadability
- Why: go/packages needs the dependencies without network (D-025).
- Depends on: none (clone exists)
- Touches: `cmd/callgraph-gt/`, `docs/plan/decisions.md` (D-025)
- Details: inside `data/kubernetes` find the invocation that makes `go list` resolve everything offline: try `GOFLAGS=-mod=vendor`, else the workspace equivalent (`go.work` with staging modules, per the repo's layout). Implement in `callgraph-gt` the flag/env set it needs (`packages.Config{Env: ...}`), and a `--check` flag that runs the `go list -deps` equivalent. Record the working invocation in D-025.
- Acceptance: with network disabled (turn off Wi-Fi or `GOPROXY=off GOFLAGS=-mod=vendor`), `(cd data/kubernetes && go list -deps ./pkg/scheduler/...)` exits 0; `go run ./cmd/callgraph-gt --check` exits 0 and prints the package count for each slice root including `staging/src/k8s.io/client-go`; D-025 holds the working command.
- Tests: unit test that builds the env slice for a given mode; live check is manual.
- Out of scope: building the call graph.
- Owner: agent

### S2-T06 - callgraph-gt: VTA graph for the slice
- Why: ground truth for structural labels and edge metrics (D-011).
- Depends on: T01, T05
- Touches: `cmd/callgraph-gt/`
- Details: `packages.Load` with `NeedName|NeedFiles|NeedSyntax|NeedTypes|NeedTypesInfo|NeedImports|NeedDeps` for the slice roots; `ssautil.AllPackages` (`ssa.InstantiateGenerics`), `prog.Build()`; call graph = `vta.CallGraph(ssautil.AllFunctions(prog), cha.CallGraph(prog))`. Keep only edges whose caller AND callee are in slice files (excluded files drop out). Map each SSA function to a `symbols.qualified_name` (receiver pointer-ness preserved; closures attributed to the enclosing function; synthetic wrappers/thunks dropped and counted). `call_kind` from SSA: static function -> `func`; static method -> `concrete_method`; `Call.IsInvoke()` -> `interface_method`; dynamic callee -> `func_value`. Replace all `source='gopackages'` rows in one transaction; print edge counts by kind and unmapped count. Memory fallback if > 20GB RSS: load slice packages with `NeedDeps` only for the slice and treat out-of-slice callees as external leaves (document in Results).
- Acceptance: `go run ./cmd/callgraph-gt` exits 0 and `select call_kind,count(*) from edges where source='gopackages' group by 1` shows all four kinds; running twice gives identical counts; peak RSS (`/usr/bin/time -l`) recorded.
- Tests: golden test on a tiny module fixture (testdata module with an interface, a func value, a concrete method) verifying the four kinds; fixture is in `cmd/callgraph-gt/testdata/` (committed).
- Out of scope: serving; use in the server (forbidden).
- Owner: agent

### S2-T07 - `eval callgraph` precision/recall report
- Why: honest call-graph quality numbers by call kind ([eval-protocol.md#call-graph-metrics](../eval-protocol.md#call-graph-metrics)).
- Depends on: T04, T06
- Touches: `cmd/eval/callgraph.go`, `eval/results/callgraph/`
- Details: compare `(caller, callee)` pairs for in-slice callers. For each GT edge: matched if a treesitter edge with the same pair exists (exact/name_only) or the callee is among ambiguous candidates. Precision = matched tree-sitter edges / tree-sitter edges whose callee is in-slice and resolved; recall = matched GT edges / GT edges. Break down by GT `call_kind`; also report ambiguous-rate and mean candidate-set size per kind. Output JSON `eval/results/callgraph/result.json` and a markdown table to stdout.
- Acceptance: `go run ./cmd/eval callgraph` exits 0 and writes the JSON with fields `precision`, `recall`, `by_kind.{func,concrete_method,interface_method,func_value}.{precision,recall,ambiguous_rate}`, `counts`; unit test on hand-built edge sets passes.
- Tests: unit tests with synthetic edge sets (perfect, all-ambiguous, missing); golden JSON.
- Out of scope: general eval harness (S4).
- Owner: agent

### S2-T08 - Error analysis of wrong edges
- Why: explain where tree-sitter loses, for the README and for header/structural quality.
- Depends on: T07
- Touches: this file (Results)
- Details: sample 30 false positives and 30 false negatives (seed 11) stratified by kind; classify cause (unknown receiver type, embedded struct, generic, closure, build-tag, dynamic). Fix only if a cause accounts for > 20% of errors and the fix is < 1.5h; otherwise record as a known limitation. Re-run T07 if fixed.
- Acceptance: Results has a cause table summing to 60 samples; any applied fix has a before/after of `eval callgraph`.
- Tests: golden test added for any fix.
- Out of scope: perfect resolution.
- Owner: agent

## Measurements to record
- `eval/results/callgraph/result.json` (precision, recall, ambiguity per kind), and copied headline numbers in Results.
- Wall time of `graph` and `callgraph-gt`; edge counts by source/resolution/kind; GT peak RSS ([eval-protocol.md#indexing-metrics](../eval-protocol.md#indexing-metrics)).

## Exit criteria / Definition of Done
Both graphs populated and re-runnable; D-025 filled; call-graph report recorded; ROADMAP updated.

## Risks + mitigations
- VTA over the slice does not fit memory or is too slow: fallback in T06; run alone (pause the summary run per S1 procedure if RAM is tight).
- Import alias/type heuristics too weak: T08 decides whether to improve; limitations are reported honestly.
- Staging paths: client-go imports as `k8s.io/client-go/...`; handle the vendor symlink mapping in the package-path function (Q-05).

## Demo check
`indexer graph --stats`, `callgraph-gt`, then `eval callgraph` prints the precision/recall table by call kind.

## Results
(empty)
