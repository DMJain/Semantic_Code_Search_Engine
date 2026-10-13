# S2 - Call graph

> Task list below is being superseded by vertical-slice tickets under `.scratch/S2/` (decisions D-073 to D-077 change the edge model). Migration number is 00005.

## Goal
Build the tree-sitter call graph with resolution confidence, build the VTA ground-truth graph for the slice, and measure tree-sitter edge precision/recall by call kind.

## Entry criteria
S1 done: symbols populated, slice final (D-005), summary run launched (it keeps running in the background; pause it for heavy jobs per [S1 overnight procedure](S1-corpus-symbols.md#overnight-run-procedure)). S2 does no summarization.

## Links
[architecture.md#stage-graph](../architecture.md#stage-graph), [#stage-callgraph-gt](../architecture.md#stage-callgraph-gt), [#data-model](../architecture.md#data-model); [eval-protocol.md#call-graph-metrics](../eval-protocol.md#call-graph-metrics). Decisions: D-011, D-019, D-023, D-025, D-058.

## Tasks

### S2-T01 - Migration 00005: edge columns
- Why: precision/recall by call kind and evidence lines need them.
- Depends on: none
- Touches: `internal/store/migrations/00005_edges_columns.sql`
- Details: `ALTER TABLE edges ADD COLUMN call_line integer, ADD COLUMN call_kind text CHECK (call_kind IN ('func','concrete_method','interface_method','func_value','unknown'));` `ALTER TABLE files ADD COLUMN graphed_sha text;` `CREATE UNIQUE INDEX edges_uniq ON edges (source, caller_id, callee_raw, call_line, coalesce(callee_id, 0));` Down reverses. The graph command inserts with `ON CONFLICT DO NOTHING` (two identical calls on one line give one row).
- Acceptance: `make migrate` ok; `\d edges` shows both columns and the index; `make migrate-down migrate-up` exits 0 and `\d edges` is unchanged.
- Tests: extend the migration integration test with duplicate-edge insert rejected.
- Out of scope: populating.
- Owner: agent

### S2-T02 - Call-site extraction: import aliases, receivers, parameters
- Why: resolution needs the receiver's type; raw names alone are not enough (D-019).
- Depends on: T01
- Touches: `internal/graph/extract.go`
- Details: re-parse a file with tree-sitter and produce per function `[]CallSite{Raw, Qualifier, Name, Line, Kind, ReceiverType}`. Per file: import alias map (`alias -> import path`, dot and blank imports ignored). Per function: a type environment limited to the receiver (`s *Scheduler`) and the parameters. `Kind` in this task: qualifier is an import alias -> `func`; everything else `unknown` (final classification in T03). Unknown types stay unknown (never guessed).
- Acceptance: `go test -count=1 ./internal/graph/...` golden tests pass for a fixture with aliased imports, dot/blank imports, receiver-typed calls, parameter-typed calls, method chains and shadowed identifiers.
- Tests: golden JSON of call sites for the fixture files.
- Out of scope: local variables and struct fields (T03); resolution to symbol IDs (T04).
- Owner: agent

### S2-T03 - Call-site extraction: local types, struct fields, call kinds
- Why: most Kubernetes calls go through locals and fields (`s.cache.AssumePod`); without them everything is `unknown` (D-019).
- Depends on: T02
- Touches: `internal/graph/extract.go`, `internal/graph/types.go`
- Details: extend the per-function type environment with `var x T`, `x := T{...}` / `&T{...}`, `x := NewT(...)` where `NewT` is a same-package function whose single declared return type is known, and struct field types of known same-package structs (`s.cache` -> field type). Embedded structs stay unknown. Final `Kind`: qualifier is an import alias -> `func`; receiver type known concrete -> `concrete_method`; known interface type -> `interface_method`; call on a local identifier typed as func -> `func_value`; else `unknown`.
- Acceptance: `go test -count=1 ./internal/graph/...` golden tests pass for a fixture covering each Kind, closures, shadowed variables and embedded structs left unknown.
- Tests: golden JSON of call sites.
- Out of scope: resolution to symbol IDs (T04).
- Owner: agent

### S2-T04 - Resolution
- Why: converts call sites to edges with honest confidence.
- Depends on: T03
- Touches: `internal/graph/resolve.go`
- Details: input call sites + a symbol index (`qualified_name`, `name`, `receiver`, package). Order: (1) same-package function by name -> exact; (2) `alias.Name` via import map to a symbol in that package -> exact; (3) method with known concrete receiver type -> `(*T).Name` or `(T).Name` -> exact; (4) known interface receiver type -> candidates = all methods named `Name` whose receiver type satisfies the method name set (name-only match on method names declared in the interface); one edge per candidate, `ambiguous` (D-023); (5) unknown receiver -> all symbols with that method name in the same package -> `name_only` if exactly one, else `ambiguous`. No match (zero candidates): store one edge with `callee_id NULL`, `callee_raw` set, resolution `name_only`. External (non-indexed) callees also produce NULL-callee edges so recall accounting is complete.
- Acceptance: golden tests: same-package call -> exact; aliased import -> exact; concrete method -> exact; interface call -> N ambiguous rows with identical `callee_raw`; unresolved external -> NULL callee row.
- Tests: golden resolution fixtures, one per rule, including a name collision across two packages.
- Out of scope: DB writes (T05).
- Owner: agent

### S2-T05 - `indexer graph` command
- Why: stage wiring, idempotent and resumable.
- Depends on: T04
- Touches: `cmd/indexer/graph.go`, `cmd/indexer/main.go` (add `graph`)
- Details: files where `graphed_sha IS DISTINCT FROM sha` (all indexed files, not only slice, so the structural retriever can serve the whole repo). Per file transaction: delete edges (`source='treesitter'`) of its symbols, insert new ones with `ON CONFLICT DO NOTHING`, set `graphed_sha`. Load the symbol index once into memory. Flags `--limit`, `--stats`. SIGINT-safe. Print edges by resolution, by `call_kind`, % ambiguous, NULL-callee count.
- Acceptance: `indexer graph` then again prints `0 files to graph`; INT-and-rerun yields the same `select count(*) from edges where source='treesitter'` as an uninterrupted run; `select resolution,count(*) from edges group by 1` shows all three values.
- Tests: guarded integration idempotency/resume test on a fixture tree.
- Out of scope: ground truth.
- Owner: agent

### S2-T06 - callgraph-gt: offline loadability and identical file set
- Why: go/packages needs the dependencies without network (D-025) and must see exactly the files the walk indexed (D-058).
- Depends on: S1-T13
- Touches: `cmd/callgraph-gt/`, `docs/plan/decisions.md` (D-025)
- Details: inside `data/kubernetes` find the invocation that makes `go list` resolve everything offline. Start from `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=vendor` (Kubernetes' own scripts disable the workspace); only if that fails try the workspace equivalent. Implement in `callgraph-gt` the env set (`packages.Config{Env: ...}`) and a `--check` flag that runs the `go list -deps` equivalent and a file-set comparison: the set of slice `.go` files loaded by `go/packages` for the four slice roots (non-test, non-generated per the walk predicates) must equal `select path from files where in_slice` from `walk`. Record the working invocation in D-025.
- Acceptance: with network disabled (turn off Wi-Fi or `GOPROXY=off`), `(cd data/kubernetes && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=vendor go list -deps ./pkg/scheduler/...)` exits 0; `go run ./cmd/callgraph-gt --check` exits 0, prints the package count for each slice root including `staging/src/k8s.io/client-go`, and prints `file-set: equal (N files)`; any difference exits non-zero listing the first 20 paths of each side; D-025 holds the working command.
- Tests: unit test that builds the env slice and a unit test of the set-comparison on synthetic sets; the live check is manual.
- Out of scope: building the call graph.
- Owner: agent

### S2-T07 - callgraph-gt: VTA graph for the slice
- Why: ground truth for structural labels and edge metrics (D-011).
- Depends on: T01, T06
- Touches: `cmd/callgraph-gt/`
- Details: `packages.Load` with `NeedName|NeedFiles|NeedSyntax|NeedTypes|NeedTypesInfo|NeedImports|NeedDeps` for the slice roots; `ssautil.AllPackages` (`ssa.InstantiateGenerics`), `prog.Build()`; call graph = `vta.CallGraph(ssautil.AllFunctions(prog), cha.CallGraph(prog))`. Keep only edges whose caller AND callee are in slice files (excluded files drop out). Map each SSA function to a `symbols.qualified_name` (receiver pointer-ness preserved; closures attributed to the enclosing function; synthetic wrappers/thunks dropped and counted). `call_kind` from SSA: static function -> `func`; static method -> `concrete_method`; `Call.IsInvoke()` -> `interface_method`; dynamic callee -> `func_value`. Insert each edge with `callee_raw` = the callee's qualified name and `call_line` = the call site line (0 if unavailable). Replace all `source='gopackages'` rows in one transaction; print edge counts by kind and unmapped count. Memory fallback if > 20GB RSS: load slice roots with `NeedSyntax|NeedTypes|NeedTypesInfo` and their dependencies with `NeedTypes` only (no syntax), and treat out-of-slice callees as external leaves (document in Results).
- Acceptance: `go run ./cmd/callgraph-gt` exits 0 and `select call_kind,count(*) from edges where source='gopackages' group by 1` shows all four kinds; running twice gives identical counts; `--check` (T06) still prints `file-set: equal`; peak RSS (`/usr/bin/time -l`) recorded.
- Tests: golden test on a tiny module fixture (testdata module with an interface, a func value, a concrete method) verifying the four kinds; fixture is in `cmd/callgraph-gt/testdata/` (committed).
- Out of scope: serving; use in the server (forbidden).
- Owner: agent

### S2-T08 - `eval callgraph` precision/recall report
- Why: honest call-graph quality numbers by call kind ([eval-protocol.md#call-graph-metrics](../eval-protocol.md#call-graph-metrics)).
- Depends on: T05, T07
- Touches: `cmd/eval/callgraph.go`, `eval/results/callgraph/`
- Details: compare `(caller, callee)` pairs for in-slice callers. For each GT edge: matched if a treesitter edge with the same pair exists (exact/name_only) or the callee is among ambiguous candidates. Precision counts one unit per call site (an ambiguous call is a true positive if the GT callee is among its candidates): matched call sites / tree-sitter call sites whose candidates are in-slice; recall = matched GT edges / GT edges. Break down by GT `call_kind`; also report ambiguous-rate and mean candidate-set size per kind. Output JSON `eval/results/callgraph/result.json` and a markdown table to stdout.
- Acceptance: `go run ./cmd/eval callgraph` exits 0 and writes the JSON with fields `precision`, `recall`, `by_kind.{func,concrete_method,interface_method,func_value}.{precision,recall,ambiguous_rate}`, `counts`; unit test on hand-built edge sets passes.
- Tests: unit tests with synthetic edge sets (perfect, all-ambiguous, missing); golden JSON.
- Out of scope: general eval harness (S4).
- Owner: agent

### S2-T09 - Error analysis of wrong edges
- Why: explain where tree-sitter loses, for the README and for header/structural quality.
- Depends on: T08
- Touches: this file (Results)
- Details: sample 30 false positives and 30 false negatives (seed 11) stratified by kind; classify cause (unknown receiver type, embedded struct, generic, closure, build-tag, dynamic). Fix only if a cause accounts for > 20% of errors and the fix changes at most one file in `internal/graph/` and 150 lines; otherwise record as a known limitation. Re-run T08 if fixed.
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
- VTA over the slice does not fit memory or is too slow: fallback in T07; run alone (pause the summary run per S1 procedure if RAM is tight).
- Import alias/type heuristics too weak: T09 decides whether to improve; limitations are reported honestly.
- Staging paths: client-go imports as `k8s.io/client-go/...`; handle the vendor symlink mapping in the package-path function (Q-05).
- Build tags: tree-sitter and `go/packages` see the same files only because both use the linux/amd64 context (D-058); T06 checks it.

## Demo check
`indexer graph --stats`, `callgraph-gt`, then `eval callgraph` prints the precision/recall table by call kind.

## Results

### GT memory measurement (S2 ticket 01, Q-04, Q-05)

Run alone on 2026-10-13 with the model server stopped, no summary or embedding
job running and no other session on the machine; Go runtime memory limit
`GOMEMLIMIT=14GiB`. Command:

```
set -a; source .env; set +a
GOMEMLIMIT=14GiB /usr/bin/time -l go run ./cmd/callgraph-gt --measure --out eval/results/callgraph/gt-memory.json
```

Result metadata in `eval/results/callgraph/gt-memory.json`:

| field | value |
| --- | --- |
| peak RSS (`peak_rss_bytes`) | 5,407,965,184 bytes (5.04 GiB); `/usr/bin/time -l` agrees (5,407,997,952) |
| wall time | 13,242 ms (~13.2 s) |
| packages loaded | 332 |
| SSA functions | 263,605 |
| VTA edges | 39,616 |
| in-slice edges (caller and callee both in slice files) | 14,469 |
| GOMEMLIMIT | 14GiB |
| offline load (`GOPROXY=off GOFLAGS=-mod=readonly`) | **FAILED** (`k8s.io/klog/v2@v2.140.0: module lookup disabled by GOPROXY=off`) |

Peak RSS is far under the 16 GiB escalation threshold, so no escalation report
is needed and Q-04 is closed. The VTA input is the slice functions plus the
initial (CHA) call graph only. `select count(*) from edges` was 0 before and 0
after the run: the measurement writes no database row. `go run
./cmd/callgraph-gt --check` still exits 0 and prints `file-set: equal (876
files)`. The offline attempt with `GOPROXY=off GOFLAGS=-mod=readonly` fails
because `k8s.io/klog/v2@v2.140.0` is not in the module cache, so Q-05 stays
open with that reason (D-025 records the working online invocation; refs
D-071).


### Tree-sitter tracer: plain calls for one package (S2 ticket 02)

Chosen package: `k8s.io/kubernetes/pkg/kubelet/envvars`.

The tracer package is chosen by rule: rank the in-slice packages by function-plus-method
count (ties by import path ascending), dry-run the extractor with no writes from the
lowest count upward with `indexer graph --package <p> --dry-run --stats`, and take the
first with at least five same-package plain-function call sites resolving `exact` and at
least one `skipped_builtin`. The first match is `pkg/kubelet/envvars` (3 declared
functions, 5 exact edges, 12 skipped builtins). Its dry run:

```
2 files to graph
would graph 2 files, 5 edges
edges stored: 5
  resolution exact: 5
  resolution name_only: 0
  resolution ambiguous: 0
  resolution unresolved: 0
  reason external: 0
  reason no_match: 0
  reason unknown_receiver: 0
  reason func_value: 0
  reason candidates_capped: 0
call kind func: 5
call kind concrete_method: 0
call kind interface_method: 0
call kind func_value: 0
call kind unknown: 0
percent ambiguous: 0.0%
unresolved: 0
skipped_builtin: 12
skipped_conversion: 0
deferred_selector: 15
deferred_local: 0
deferred_interface: 0
interfaces_partial: 0
invariant: call sites 32 = stored 5 + skipped 12 + deferred 15 [ok]
```

The ranked in-slice packages (294), function-plus-method count, ties by import path ascending:

| # | package | func+method |
| --- | --- | --- |
| 1 | `k8s.io/kubernetes/pkg/kubelet` | 348 |
| 2 | `k8s.io/kubernetes/pkg/kubelet/cm` | 279 |
| 3 | `k8s.io/kubernetes/pkg/kubelet/container/testing` | 271 |
| 4 | `k8s.io/kubernetes/pkg/kubelet/kuberuntime` | 258 |
| 5 | `k8s.io/kubernetes/pkg/kubelet/cm/testing` | 239 |
| 6 | `k8s.io/kubernetes/pkg/scheduler/testing` | 237 |
| 7 | `k8s.io/kubernetes/pkg/scheduler/framework` | 191 |
| 8 | `k8s.io/kubernetes/pkg/scheduler/backend/queue` | 182 |
| 9 | `k8s.io/kubernetes/pkg/scheduler/backend/cache` | 171 |
| 10 | `k8s.io/kubernetes/pkg/controller/job` | 165 |
| 11 | `k8s.io/kubernetes/pkg/scheduler/framework/runtime` | 160 |
| 12 | `k8s.io/kubernetes/pkg/scheduler` | 140 |
| 13 | `k8s.io/kubernetes/pkg/kubelet/cm/cpumanager` | 138 |
| 14 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/dynamicresources` | 123 |
| 15 | `k8s.io/kubernetes/pkg/controller/garbagecollector` | 112 |
| 16 | `k8s.io/kubernetes/pkg/controller/statefulset` | 111 |
| 17 | `k8s.io/kubernetes/pkg/scheduler/testing/framework` | 110 |
| 18 | `k8s.io/kubernetes/pkg/controller` | 106 |
| 19 | `k8s.io/kubernetes/pkg/kubelet/stats` | 106 |
| 20 | `k8s.io/kubernetes/pkg/kubelet/container` | 104 |
| 21 | `k8s.io/kubernetes/pkg/kubelet/server/stats/testing` | 99 |
| 22 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/volumebinding` | 99 |
| 23 | `k8s.io/kubernetes/pkg/kubelet/volumemanager/cache` | 94 |
| 24 | `k8s.io/kubernetes/pkg/scheduler/framework/preemption` | 94 |
| 25 | `k8s.io/kubernetes/pkg/controller/volume/persistentvolume` | 92 |
| 26 | `k8s.io/kubernetes/pkg/kubelet/cm/memorymanager` | 92 |
| 27 | `k8s.io/kubernetes/pkg/kubelet/images/pullmanager` | 90 |
| 28 | `k8s.io/kubernetes/pkg/kubelet/eviction` | 88 |
| 29 | `k8s.io/kubernetes/pkg/kubelet/cm/topologymanager` | 86 |
| 30 | `k8s.io/kubernetes/pkg/kubelet/pod/testing` | 84 |
| 31 | `k8s.io/kubernetes/pkg/controller/podautoscaler` | 83 |
| 32 | `k8s.io/kubernetes/pkg/kubelet/cm/devicemanager` | 77 |
| 33 | `k8s.io/kubernetes/pkg/kubelet/apis/podresources/testing` | 75 |
| 34 | `k8s.io/kubernetes/pkg/kubelet/cm/dra` | 63 |
| 35 | `k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/state` | 62 |
| 36 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/noderesources` | 62 |
| 37 | `k8s.io/kubernetes/pkg/controller/daemon` | 57 |
| 38 | `k8s.io/kubernetes/pkg/kubelet/cadvisor/testing` | 56 |
| 39 | `k8s.io/kubernetes/pkg/controller/devicetainteviction` | 54 |
| 40 | `k8s.io/kubernetes/pkg/controller/deployment/util` | 53 |
| 41 | `k8s.io/kubernetes/pkg/controller/deployment` | 52 |
| 42 | `k8s.io/kubernetes/pkg/kubelet/server` | 52 |
| 43 | `k8s.io/kubernetes/pkg/kubelet/cm/memorymanager/state` | 51 |
| 44 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/interpodaffinity` | 51 |
| 45 | `k8s.io/kubernetes/pkg/controller/endpointslicemirroring` | 50 |
| 46 | `k8s.io/kubernetes/pkg/kubelet/cm/devicemanager/plugin/v1beta1` | 50 |
| 47 | `k8s.io/kubernetes/pkg/kubelet/config` | 50 |
| 48 | `k8s.io/kubernetes/pkg/controller/storageversionmigrator` | 48 |
| 49 | `k8s.io/kubernetes/pkg/kubelet/status` | 48 |
| 50 | `k8s.io/kubernetes/pkg/controller/resourceclaim` | 46 |
| 51 | `k8s.io/kubernetes/pkg/controller/cronjob` | 45 |
| 52 | `k8s.io/kubernetes/pkg/controller/serviceaccount` | 45 |
| 53 | `k8s.io/kubernetes/pkg/controller/volume/selinuxwarning` | 45 |
| 54 | `k8s.io/kubernetes/pkg/scheduler/metrics` | 45 |
| 55 | `k8s.io/kubernetes/pkg/controller/volume/attachdetach` | 44 |
| 56 | `k8s.io/kubernetes/pkg/kubelet/allocation/state` | 44 |
| 57 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/podtopologyspread` | 43 |
| 58 | `k8s.io/kubernetes/pkg/controller/volume/attachdetach/cache` | 41 |
| 59 | `k8s.io/kubernetes/pkg/kubelet/images` | 41 |
| 60 | `k8s.io/kubernetes/pkg/controller/disruption` | 39 |
| 61 | `k8s.io/kubernetes/pkg/controller/replication` | 39 |
| 62 | `k8s.io/kubernetes/pkg/kubelet/prober` | 39 |
| 63 | `k8s.io/kubernetes/pkg/controller/volume/attachdetach/testing` | 38 |
| 64 | `k8s.io/kubernetes/pkg/kubelet/allocation` | 38 |
| 65 | `k8s.io/kubernetes/pkg/kubelet/volumemanager` | 38 |
| 66 | `k8s.io/kubernetes/pkg/controller/replicaset` | 34 |
| 67 | `k8s.io/kubernetes/pkg/kubelet/podcertificate` | 33 |
| 68 | `k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/topology` | 32 |
| 69 | `k8s.io/kubernetes/pkg/kubelet/pleg` | 32 |
| 70 | `k8s.io/kubernetes/pkg/kubelet/util/manager` | 32 |
| 71 | `k8s.io/kubernetes/pkg/controller/util/protectionutil` | 31 |
| 72 | `k8s.io/kubernetes/pkg/kubelet/lifecycle` | 31 |
| 73 | `k8s.io/kubernetes/pkg/scheduler/util/assumecache` | 30 |
| 74 | `k8s.io/kubernetes/pkg/controller/nodelifecycle` | 29 |
| 75 | `k8s.io/kubernetes/pkg/controller/tainteviction` | 29 |
| 76 | `k8s.io/kubernetes/pkg/controller/testutil` | 29 |
| 77 | `k8s.io/kubernetes/pkg/controller/volume/expand` | 29 |
| 78 | `k8s.io/kubernetes/pkg/controller/endpoint` | 27 |
| 79 | `k8s.io/kubernetes/pkg/controller/endpointslice` | 27 |
| 80 | `k8s.io/kubernetes/pkg/controller/resourcepoolstatusrequest` | 27 |
| 81 | `k8s.io/kubernetes/pkg/controller/volume/pvcprotection` | 27 |
| 82 | `k8s.io/kubernetes/pkg/kubelet/metrics/collectors` | 27 |
| 83 | `k8s.io/kubernetes/pkg/kubelet/pluginmanager/pluginwatcher` | 27 |
| 84 | `k8s.io/kubernetes/pkg/kubelet/pod` | 27 |
| 85 | `k8s.io/kubernetes/pkg/kubelet/util` | 27 |
| 86 | `k8s.io/kubernetes/pkg/scheduler/apis/config/validation` | 27 |
| 87 | `k8s.io/kubernetes/pkg/controller/certificates/clustertrustbundlepublisher` | 26 |
| 88 | `k8s.io/kubernetes/pkg/controller/history` | 26 |
| 89 | `k8s.io/kubernetes/pkg/controller/namespace/deletion` | 26 |
| 90 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/nodevolumelimits` | 26 |
| 91 | `k8s.io/kubernetes/pkg/kubelet/clustertrustbundle` | 25 |
| 92 | `k8s.io/kubernetes/pkg/kubelet/cm/dra/plugin` | 25 |
| 93 | `k8s.io/kubernetes/pkg/kubelet/volumemanager/reconciler` | 25 |
| 94 | `k8s.io/kubernetes/pkg/scheduler/apis/config/v1` | 25 |
| 95 | `k8s.io/kubernetes/pkg/controller/resourcequota` | 23 |
| 96 | `k8s.io/kubernetes/pkg/kubelet/server/stats` | 22 |
| 97 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/volumerestrictions` | 22 |
| 98 | `k8s.io/kubernetes/pkg/kubelet/cm/topologymanager/bitmask` | 21 |
| 99 | `k8s.io/kubernetes/pkg/kubelet/types` | 21 |
| 100 | `k8s.io/kubernetes/pkg/scheduler/backend/api_dispatcher` | 21 |
| 101 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/gangscheduling` | 21 |
| 102 | `k8s.io/kubernetes/pkg/controller/certificates/signer` | 20 |
| 103 | `k8s.io/kubernetes/pkg/controller/volume/persistentvolume/testing` | 20 |
| 104 | `k8s.io/kubernetes/pkg/kubelet/nodeshutdown` | 20 |
| 105 | `k8s.io/kubernetes/pkg/controller/bootstrap` | 19 |
| 106 | `k8s.io/kubernetes/pkg/kubelet/apis/pods` | 19 |
| 107 | `k8s.io/kubernetes/pkg/kubelet/cadvisor` | 19 |
| 108 | `k8s.io/kubernetes/pkg/controller/nodelifecycle/scheduler` | 18 |
| 109 | `k8s.io/kubernetes/pkg/kubelet/logs` | 18 |
| 110 | `k8s.io/kubernetes/pkg/kubelet/pluginmanager/cache` | 18 |
| 111 | `k8s.io/kubernetes/pkg/kubelet/volumemanager/populator` | 18 |
| 112 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/nodeaffinity` | 18 |
| 113 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/volumezone` | 18 |
| 114 | `k8s.io/kubernetes/pkg/controller/certificates/cleaner` | 17 |
| 115 | `k8s.io/kubernetes/pkg/controller/podgc` | 17 |
| 116 | `k8s.io/kubernetes/pkg/controller/servicecidrs` | 17 |
| 117 | `k8s.io/kubernetes/pkg/controller/volume/persistentvolume/metrics` | 17 |
| 118 | `k8s.io/kubernetes/pkg/scheduler/backend/heap` | 17 |
| 119 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/tainttoleration` | 17 |
| 120 | `k8s.io/kubernetes/pkg/controller/nodeipam/ipam` | 16 |
| 121 | `k8s.io/kubernetes/pkg/controller/util/selectors` | 16 |
| 122 | `k8s.io/kubernetes/pkg/scheduler/framework/api_calls` | 16 |
| 123 | `k8s.io/kubernetes/pkg/scheduler/util` | 16 |
| 124 | `k8s.io/kubernetes/pkg/controller/ttlafterfinished` | 15 |
| 125 | `k8s.io/kubernetes/pkg/controller/util/consistency` | 15 |
| 126 | `k8s.io/kubernetes/pkg/controller/volume/vacprotection` | 15 |
| 127 | `k8s.io/kubernetes/pkg/kubelet/network/dns` | 15 |
| 128 | `k8s.io/kubernetes/pkg/kubelet/secret` | 15 |
| 129 | `k8s.io/kubernetes/pkg/kubelet/userns` | 15 |
| 130 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultpreemption` | 15 |
| 131 | `k8s.io/kubernetes/pkg/controller/storageversiongc` | 14 |
| 132 | `k8s.io/kubernetes/pkg/kubelet/configmap` | 14 |
| 133 | `k8s.io/kubernetes/pkg/controller/certificates/rootcacertpublisher` | 13 |
| 134 | `k8s.io/kubernetes/pkg/controller/clusterroleaggregation` | 13 |
| 135 | `k8s.io/kubernetes/pkg/controller/nodeipam/ipam/sync` | 13 |
| 136 | `k8s.io/kubernetes/pkg/controller/scheduling/podgroupprotection` | 13 |
| 137 | `k8s.io/kubernetes/pkg/controller/ttl` | 13 |
| 138 | `k8s.io/kubernetes/pkg/kubelet/certificate` | 13 |
| 139 | `k8s.io/kubernetes/pkg/kubelet/nodestatus` | 13 |
| 140 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/nodeports` | 12 |
| 141 | `k8s.io/kubernetes/pkg/controller/certificates` | 11 |
| 142 | `k8s.io/kubernetes/pkg/controller/util/node` | 11 |
| 143 | `k8s.io/kubernetes/pkg/controller/volume/attachdetach/reconciler` | 11 |
| 144 | `k8s.io/kubernetes/pkg/scheduler/backend/cache/debugger` | 11 |
| 145 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/nodedeclaredfeatures` | 11 |
| 146 | `k8s.io/kubernetes/pkg/controller/endpointslicemirroring/metrics` | 10 |
| 147 | `k8s.io/kubernetes/pkg/controller/volume/attachdetach/util` | 10 |
| 148 | `k8s.io/kubernetes/pkg/kubelet/apis/podresources` | 10 |
| 149 | `k8s.io/kubernetes/pkg/kubelet/cm/dra/state` | 10 |
| 150 | `k8s.io/kubernetes/pkg/kubelet/preemption` | 10 |
| 151 | `k8s.io/kubernetes/pkg/controller/daemon/util` | 9 |
| 152 | `k8s.io/kubernetes/pkg/controller/nodeipam/ipam/cidrset` | 9 |
| 153 | `k8s.io/kubernetes/pkg/controller/podautoscaler/metrics` | 9 |
| 154 | `k8s.io/kubernetes/pkg/controller/podautoscaler/monitor` | 9 |
| 155 | `k8s.io/kubernetes/pkg/controller/volume/pvprotection` | 9 |
| 156 | `k8s.io/kubernetes/pkg/controller/volume/selinuxwarning/cache` | 9 |
| 157 | `k8s.io/kubernetes/pkg/kubelet/certificate/bootstrap` | 9 |
| 158 | `k8s.io/kubernetes/pkg/kubelet/pluginmanager/operationexecutor` | 9 |
| 159 | `k8s.io/kubernetes/pkg/kubelet/token` | 9 |
| 160 | `k8s.io/kubernetes/pkg/kubelet/util/store` | 9 |
| 161 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/deferredpodscheduling` | 9 |
| 162 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/imagelocality` | 9 |
| 163 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/nodeunschedulable` | 9 |
| 164 | `k8s.io/kubernetes/pkg/controller/certificates/approver` | 8 |
| 165 | `k8s.io/kubernetes/pkg/controller/volume/attachdetach/metrics` | 8 |
| 166 | `k8s.io/kubernetes/pkg/controller/volume/attachdetach/statusupdater` | 8 |
| 167 | `k8s.io/kubernetes/pkg/controller/volume/ephemeral` | 8 |
| 168 | `k8s.io/kubernetes/pkg/kubelet/cm/containermap` | 8 |
| 169 | `k8s.io/kubernetes/pkg/kubelet/cm/devicemanager/checkpoint` | 8 |
| 170 | `k8s.io/kubernetes/pkg/kubelet/nodeshutdown/systemd` | 8 |
| 171 | `k8s.io/kubernetes/pkg/kubelet/prober/results` | 8 |
| 172 | `k8s.io/kubernetes/pkg/kubelet/status/testing` | 8 |
| 173 | `k8s.io/kubernetes/pkg/kubelet/watchdog` | 8 |
| 174 | `k8s.io/kubernetes/pkg/scheduler/framework/parallelize` | 8 |
| 175 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/nodename` | 8 |
| 176 | `k8s.io/kubernetes/pkg/scheduler/metrics/resources` | 8 |
| 177 | `k8s.io/kubernetes/pkg/kubelet/client` | 7 |
| 178 | `k8s.io/kubernetes/pkg/kubelet/metrics` | 7 |
| 179 | `k8s.io/kubernetes/pkg/kubelet/volumemanager/volumehealth` | 7 |
| 180 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/helper` | 7 |
| 181 | `k8s.io/kubernetes/pkg/controller/certificates/authority` | 6 |
| 182 | `k8s.io/kubernetes/pkg/controller/namespace` | 6 |
| 183 | `k8s.io/kubernetes/pkg/controller/serviceaccount/config/v1alpha1` | 6 |
| 184 | `k8s.io/kubernetes/pkg/controller/validatingadmissionpolicystatus` | 6 |
| 185 | `k8s.io/kubernetes/pkg/kubelet/cm/admission` | 6 |
| 186 | `k8s.io/kubernetes/pkg/kubelet/events` | 6 |
| 187 | `k8s.io/kubernetes/pkg/kubelet/prober/testing` | 6 |
| 188 | `k8s.io/kubernetes/pkg/kubelet/server/metrics` | 6 |
| 189 | `k8s.io/kubernetes/pkg/kubelet/sysctl` | 6 |
| 190 | `k8s.io/kubernetes/pkg/kubelet/util/sliceutils` | 6 |
| 191 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/podgrouppodscount` | 6 |
| 192 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/topologyaware` | 6 |
| 193 | `k8s.io/kubernetes/pkg/scheduler/profile` | 6 |
| 194 | `k8s.io/kubernetes/pkg/controller/nodeipam` | 5 |
| 195 | `k8s.io/kubernetes/pkg/controller/volume/attachdetach/populator` | 5 |
| 196 | `k8s.io/kubernetes/pkg/controller/volume/persistentvolume/config/v1alpha1` | 5 |
| 197 | `k8s.io/kubernetes/pkg/kubelet/apis/config/validation` | 5 |
| 198 | `k8s.io/kubernetes/pkg/kubelet/checkpointmanager` | 5 |
| 199 | `k8s.io/kubernetes/pkg/kubelet/checkpointmanager/testing` | 5 |
| 200 | `k8s.io/kubernetes/pkg/kubelet/kuberuntime/util` | 5 |
| 201 | `k8s.io/kubernetes/pkg/kubelet/pluginmanager/metrics` | 5 |
| 202 | `k8s.io/kubernetes/pkg/kubelet/pluginmanager/reconciler` | 5 |
| 203 | `k8s.io/kubernetes/pkg/kubelet/qos` | 5 |
| 204 | `k8s.io/kubernetes/pkg/kubelet/volumemanager/metrics` | 5 |
| 205 | `k8s.io/kubernetes/pkg/scheduler/backend/cache/fake` | 5 |
| 206 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/schedulinggates` | 5 |
| 207 | `k8s.io/kubernetes/pkg/controller/volume/selinuxwarning/translator` | 4 |
| 208 | `k8s.io/kubernetes/pkg/kubelet/apis/config/v1beta1` | 4 |
| 209 | `k8s.io/kubernetes/pkg/kubelet/kubeletconfig/configfiles` | 4 |
| 210 | `k8s.io/kubernetes/pkg/kubelet/kubeletconfig/util/codec` | 4 |
| 211 | `k8s.io/kubernetes/pkg/kubelet/oom` | 4 |
| 212 | `k8s.io/kubernetes/pkg/kubelet/runtimeclass` | 4 |
| 213 | `k8s.io/kubernetes/pkg/kubelet/util/cache` | 4 |
| 214 | `k8s.io/kubernetes/pkg/scheduler/backend/api_cache` | 4 |
| 215 | `k8s.io/kubernetes/pkg/controller/apis/config/v1alpha1` | 3 |
| 216 | `k8s.io/kubernetes/pkg/controller/certificates/signer/config/v1alpha1` | 3 |
| 217 | `k8s.io/kubernetes/pkg/controller/cronjob/config/v1alpha1` | 3 |
| 218 | `k8s.io/kubernetes/pkg/controller/daemon/config/v1alpha1` | 3 |
| 219 | `k8s.io/kubernetes/pkg/controller/deployment/config/v1alpha1` | 3 |
| 220 | `k8s.io/kubernetes/pkg/controller/devicetainteviction/config/v1alpha1` | 3 |
| 221 | `k8s.io/kubernetes/pkg/controller/disruption/config/v1alpha1` | 3 |
| 222 | `k8s.io/kubernetes/pkg/controller/endpoint/config/v1alpha1` | 3 |
| 223 | `k8s.io/kubernetes/pkg/controller/endpointslice/config/v1alpha1` | 3 |
| 224 | `k8s.io/kubernetes/pkg/controller/endpointslicemirroring/config/v1alpha1` | 3 |
| 225 | `k8s.io/kubernetes/pkg/controller/garbagecollector/config/v1alpha1` | 3 |
| 226 | `k8s.io/kubernetes/pkg/controller/job/config/v1alpha1` | 3 |
| 227 | `k8s.io/kubernetes/pkg/controller/job/util` | 3 |
| 228 | `k8s.io/kubernetes/pkg/controller/namespace/config/v1alpha1` | 3 |
| 229 | `k8s.io/kubernetes/pkg/controller/nodeipam/config/v1alpha1` | 3 |
| 230 | `k8s.io/kubernetes/pkg/controller/nodeipam/ipam/test` | 3 |
| 231 | `k8s.io/kubernetes/pkg/controller/nodelifecycle/config/v1alpha1` | 3 |
| 232 | `k8s.io/kubernetes/pkg/controller/podautoscaler/config/v1alpha1` | 3 |
| 233 | `k8s.io/kubernetes/pkg/controller/podgc/config/v1alpha1` | 3 |
| 234 | `k8s.io/kubernetes/pkg/controller/replicaset/config/v1alpha1` | 3 |
| 235 | `k8s.io/kubernetes/pkg/controller/replication/config/v1alpha1` | 3 |
| 236 | `k8s.io/kubernetes/pkg/controller/resourceclaim/config/v1alpha1` | 3 |
| 237 | `k8s.io/kubernetes/pkg/controller/resourcequota/config/v1alpha1` | 3 |
| 238 | `k8s.io/kubernetes/pkg/controller/statefulset/config/v1alpha1` | 3 |
| 239 | `k8s.io/kubernetes/pkg/controller/ttlafterfinished/config/v1alpha1` | 3 |
| 240 | `k8s.io/kubernetes/pkg/controller/util/endpointslice` | 3 |
| 241 | `k8s.io/kubernetes/pkg/controller/validatingadmissionpolicystatus/config/v1alpha1` | 3 |
| 242 | `k8s.io/kubernetes/pkg/controller/volume/attachdetach/config/v1alpha1` | 3 |
| 243 | `k8s.io/kubernetes/pkg/controller/volume/common` | 3 |
| 244 | `k8s.io/kubernetes/pkg/controller/volume/ephemeral/config/v1alpha1` | 3 |
| 245 | `k8s.io/kubernetes/pkg/kubelet/checkpointmanager/checksum` | 3 |
| 246 | `k8s.io/kubernetes/pkg/kubelet/checkpointmanager/testing/example_checkpoint_formats/v1` | 3 |
| 247 | `k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/state/testing` | 3 |
| 248 | `k8s.io/kubernetes/pkg/kubelet/cm/util` | 3 |
| 249 | `k8s.io/kubernetes/pkg/kubelet/envvars` | 3 |
| 250 | `k8s.io/kubernetes/pkg/kubelet/pluginmanager` | 3 |
| 251 | `k8s.io/kubernetes/pkg/kubelet/runtimeclass/testing` | 3 |
| 252 | `k8s.io/kubernetes/pkg/kubelet/util/queue` | 3 |
| 253 | `k8s.io/kubernetes/pkg/kubelet/util/swap` | 3 |
| 254 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder` | 3 |
| 255 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort` | 3 |
| 256 | `k8s.io/kubernetes/pkg/controller/apis/config/scheme` | 2 |
| 257 | `k8s.io/kubernetes/pkg/controller/devicetainteviction/metrics` | 2 |
| 258 | `k8s.io/kubernetes/pkg/controller/resourceclaim/metrics` | 2 |
| 259 | `k8s.io/kubernetes/pkg/controller/volume/persistentvolume/options` | 2 |
| 260 | `k8s.io/kubernetes/pkg/kubelet/apis/config` | 2 |
| 261 | `k8s.io/kubernetes/pkg/kubelet/apis/grpc` | 2 |
| 262 | `k8s.io/kubernetes/pkg/kubelet/checkpointmanager/errors` | 2 |
| 263 | `k8s.io/kubernetes/pkg/kubelet/kubeletconfig/util/test` | 2 |
| 264 | `k8s.io/kubernetes/pkg/kubelet/stats/pidlimit` | 2 |
| 265 | `k8s.io/kubernetes/pkg/kubelet/util/format` | 2 |
| 266 | `k8s.io/kubernetes/pkg/kubelet/util/ioutils` | 2 |
| 267 | `k8s.io/kubernetes/pkg/scheduler/apis/config` | 2 |
| 268 | `k8s.io/kubernetes/pkg/scheduler/apis/config/scheme` | 2 |
| 269 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/testing` | 2 |
| 270 | `k8s.io/kubernetes/pkg/controller/apis/config` | 1 |
| 271 | `k8s.io/kubernetes/pkg/controller/apis/config/fuzzer` | 1 |
| 272 | `k8s.io/kubernetes/pkg/controller/cronjob/metrics` | 1 |
| 273 | `k8s.io/kubernetes/pkg/controller/daemon/metrics` | 1 |
| 274 | `k8s.io/kubernetes/pkg/controller/garbagecollector/metrics` | 1 |
| 275 | `k8s.io/kubernetes/pkg/controller/job/metrics` | 1 |
| 276 | `k8s.io/kubernetes/pkg/controller/podgc/metrics` | 1 |
| 277 | `k8s.io/kubernetes/pkg/controller/replicaset/metrics` | 1 |
| 278 | `k8s.io/kubernetes/pkg/controller/resourcepoolstatusrequest/metrics` | 1 |
| 279 | `k8s.io/kubernetes/pkg/controller/statefulset/metrics` | 1 |
| 280 | `k8s.io/kubernetes/pkg/controller/tainteviction/metrics` | 1 |
| 281 | `k8s.io/kubernetes/pkg/controller/ttlafterfinished/metrics` | 1 |
| 282 | `k8s.io/kubernetes/pkg/controller/volume/ephemeral/metrics` | 1 |
| 283 | `k8s.io/kubernetes/pkg/controller/volume/selinuxwarning/internal/parse` | 1 |
| 284 | `k8s.io/kubernetes/pkg/kubelet/apis/config/fuzzer` | 1 |
| 285 | `k8s.io/kubernetes/pkg/kubelet/apis/config/scheme` | 1 |
| 286 | `k8s.io/kubernetes/pkg/kubelet/apis/config/v1alpha1` | 1 |
| 287 | `k8s.io/kubernetes/pkg/kubelet/cm/qos` | 1 |
| 288 | `k8s.io/kubernetes/pkg/kubelet/eviction/api` | 1 |
| 289 | `k8s.io/kubernetes/pkg/kubelet/util/env` | 1 |
| 290 | `k8s.io/kubernetes/pkg/scheduler/apis/config/latest` | 1 |
| 291 | `k8s.io/kubernetes/pkg/scheduler/apis/config/testing` | 1 |
| 292 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins` | 1 |
| 293 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/feature` | 1 |
| 294 | `k8s.io/kubernetes/pkg/scheduler/framework/plugins/volumebinding/metrics` | 1 |

Real run (writes), then a rerun:

```
$ go run ./cmd/indexer graph --package k8s.io/kubernetes/pkg/kubelet/envvars --stats
2 files to graph
graphed 2 files, stored 5 edges
edges stored: 5
  resolution exact: 5
  resolution name_only: 0
  resolution ambiguous: 0
  resolution unresolved: 0
  reason external: 0
  reason no_match: 0
  reason unknown_receiver: 0
  reason func_value: 0
  reason candidates_capped: 0
call kind func: 5
call kind concrete_method: 0
call kind interface_method: 0
call kind func_value: 0
call kind unknown: 0
percent ambiguous: 0.0%
unresolved: 0
skipped_builtin: 12
skipped_conversion: 0
deferred_selector: 15
deferred_local: 0
deferred_interface: 0
interfaces_partial: 0
invariant: call sites 32 = stored 5 + skipped 12 + deferred 15 [ok]
```

The rerun prints identical counts. `select count(*) from edges where
(resolution='unresolved') <> (callee_id is null)` returns 0. All 15 deferred call sites in
this package are selectors; none are local identifier calls, so the interim `deferred_local`
counter is 0 here and locals cannot be told apart from unresolved identifiers in this slice
only in that a locally declared identifier call is deferred rather than stored.

### Freshness and whole-repo trial run (S2 ticket 03)

Freshness (D-076). A file is stale iff its stored `graphed_sha` differs from its
current `sha` or its stored `graphed_symbols_fp` differs from the current
fingerprint, where the fingerprint is
`graph.Fingerprint(allIndexedSymbols, graph.ResolverVersion)` computed once per
run. `selectGraphFiles` reads the selected files (all indexed files, or one
package with `--package`) from the database, and staleness is evaluated in Go over
that result set (not a DB-side `WHERE`); this is equivalent to a
`graphed_sha IS DISTINCT FROM sha OR graphed_symbols_fp IS DISTINCT FROM $fp`
predicate. `graph` then processes only the stale files and prints
`N files to graph`; a stale file's whole package is still extracted so the
declared-name index covers every file of the package, but only the stale files are
analyzed and written (per-file transaction, SIGINT boundary unchanged). `--limit N`
counts stale files written. `parse` never writes `edges`, `files.graphed_sha` or
`files.graphed_symbols_fp`.

`graph --status` reasons. For every stale file it prints the path and the reason(s):
`never graphed` (`graphed_sha IS NULL`, reported alone), `content changed`
(`graphed_sha` differs from `sha`) and `symbols changed` (`graphed_symbols_fp`
differs from the fingerprint). A file can carry more than one reason, so the
per-reason counts are independent and can sum above the file total; the total and
the list are capped at 50 (`... (N more)`), and `0 files stale` when none. Example
before the trial (6633 indexed files, 2 already graphed in ticket 02):

```
6631 files stale
  reasons (a file may have more than one, so these sum above the total):
    never graphed: 6631
    content changed: 0
    symbols changed: 0
  staging/src/k8s.io/api/doc.go: never graphed
  ...
```

Staleness tests (fixture plus real Postgres, `TestGraphStaleness*`). (i) A callee
removed and re-parsed: the cascade deletes its edge with no CHECK violation
(shape-violating rows = 0); the caller's content-unchanged file is reported
`symbols changed`, and the removed callee's file carries both `content changed`
and `symbols changed` (so the independent per-reason counts, `content changed: 1`
and `symbols changed: 2`, sum above the 2-file total). (ii) A symbol added that
makes a previously `no_match` identifier resolvable: the fingerprint changes, so a
file whose content did not change (`z.go`) is reported stale (`symbols changed`)
and the call becomes `exact`. (iii) A body-only edit: only the edited file is
stale, reason `content changed`, `symbols changed: 0`, and no other file is listed.

Body-only-edit interpretation. A body-only edit changes the file's content hash,
so the edited file itself is stale by `content changed`; the ticket's "makes
nothing newly stale" is read as no OTHER file becoming stale, because the
symbol-set fingerprint (id, kind, package, qualified name, receiver of all
symbols, plus the resolver version) is unchanged.

Other tests. `TestFingerprintVersionAndInputs` proves the fingerprint is
deterministic, changes when `ResolverVersion` is bumped, ignores `StartLine`
(line numbers), and changes when a symbol's kind changes or a symbol is added.
`TestGraphStageRerunIdempotent` proves a second `graph` prints `0 files to graph`
and rebuilds nothing. `TestGraphStageInterruptedThenRerunSameCount` proves an
interrupted run (simulated with `--limit 1`, leaving the rest stale) followed by a
full run yields the same `select count(*) from edges where source='treesitter'`
as an uninterrupted run on the fixture tree. `TestGraphParseLeavesGraphState`
proves a real re-parse leaves the stored edges and both markers unchanged (and
asserts the run stored edges first, so it cannot pass vacuously); a vanished
callee's edge is removed by the cascade FK (D-076/story 31), exercised by
`TestGraphStalenessCalleeRemoved`.

Whole-repo trial run (dev DB, for performance only). This run's edges are mostly
deferred and will go stale as later tickets bump the resolver version; the final
whole-repo run is ticket 09. Command and output:

```
$ time go run ./cmd/indexer graph --stats
6631 files to graph
graphed 6631 files, stored 25075 edges
edges stored: 25075
  resolution exact: 24580
  resolution name_only: 0
  resolution ambiguous: 0
  resolution unresolved: 495
  reason external: 0
  reason no_match: 495
  reason unknown_receiver: 0
  reason func_value: 0
  reason candidates_capped: 0
call kind func: 24580
call kind concrete_method: 0
call kind interface_method: 0
call kind func_value: 0
call kind unknown: 495
percent ambiguous: 0.0%
unresolved: 495
skipped_builtin: 33357
skipped_conversion: 339
deferred_selector: 183929
deferred_local: 3782
deferred_interface: 0
interfaces_partial: 0
invariant: call sites 246482 = stored 25075 + skipped 33696 + deferred 187711 [ok]

real 49.82s  user 6.64s  sys 2.12s
```

`select count(*) from edges where source='treesitter'` is 25080 (the 25075
written here plus the 5 rows ticket 02 left for `pkg/kubelet/envvars`, which were
fresh and skipped); shape-violating rows = 0. Afterwards:

```
$ go run ./cmd/indexer graph --status   -> 0 files stale
$ go run ./cmd/indexer graph            -> 0 files to graph
```

### Import aliases, externals and cross-package conversions (S2 ticket 04)

What changed. Each extracted file now carries an import alias map (spec story 38):
an explicit alias (`import a "p"`), or the default package name of an unaliased
import (`import "p"`); dot (`.`) and blank (`_`) imports are ignored. The default
package name is the last path segment, except a major-version module suffix `/vN`
(N >= 2), whose package name is the parent segment: `k8s.io/klog/v2` binds `klog`,
while `k8s.io/api/core/v1` (v1 is a real package name) and `v1beta1`/`v1alpha1`
segments stay as written. A call written `alias.Name` (a selector whose operand is
a bare identifier) is Rule 2: the aliased package's same-package function `Name`
is `exact` kind `func`; an import path that is not an indexed package is stored
`unresolved` reason `external`; an indexed package without the function is a
declared-type conversion (`skipped_conversion`) or `unresolved` reason `no_match`.
A qualifier that is not a known import alias, or an alias hidden by a
local/parameter (local scope wins), stays `deferred_selector`, as do receiver-style
selector and chained calls. The declared-name index is now cross-package: `graph`
scans the source of every indexed file once with a names-only tree-sitter pass
(`graph.DeclaredNames`) to build `package -> declared names (type/func/var/const)`
plus the set of indexed packages; an import path absent from that set is external.
`files.package` already stores the Go import path (`walk.ImportPath` maps
`staging/src/k8s.io/client-go/...` to `k8s.io/client-go/...`), so the staging
mapping is the existing package lookup and no vendor/staging mapping was added.
`graph.ResolverVersion` is bumped 1 -> 2, and the freshness fingerprint now covers
the declared-name index as well as the symbols (D-076 amended for this ticket), so
every file graphed before is stale. Out of scope here:
receiver/parameter/local/field types and Rules 3 to 5; the rest of
`deferred_selector` is ticket 06.

Cross-package index cost. The index scan reads and parses every indexed file on
every `graph` run, selected package or not; each of the two real runs below took
~4.4-4.8 s wall including the `go run` build and the scan of all 6633 indexed
files (names-only tree-sitter, no call extraction), so the whole-repo cost is a
single extra parse pass.

Fixture goldens (`cmd/indexer/testdata/graph/`, real Postgres,
`TestGraphStageAliasGoldens`, `TestExtractImportAliases`,
`TestExtractQualifiedCallShape`, `TestDefaultAlias`,
`TestExtractUnaliasedVersionedImport`). The `alias` package imports two packages
that both declare `Name` (the collision resolves by alias: `depone.Name` -> exact
`k8s.io/kubernetes/depone.Name`, `deptwo.Name` -> exact `deptwo.Name`), a dot
import (its name is not a same-package function, so `DotOnly()` is `no_match`), a
blank import (no alias, so the qualifier is unknown and the call is deferred), a
non-struct declared type (`typed.Type(x)` -> `skipped_conversion`, not stored), an
UNALIASED versioned import (`"k8s.io/klog/v2"`; the default alias is `klog`, and
`klog.V(2).InfoS`: the inner `klog.V` is `external`, the outer chained call is
deferred and not stored), a missed symbol in an indexed package
(`depone.Missing` -> `no_match`), a staging-style import path
(`cache "k8s.io/client-go/tools/cache"` -> exact
`k8s.io/client-go/tools/cache.SomeFunc`), and a parameter named `klog` shadowing
the alias (both calls stay deferred). `TestDefaultAlias` pins the derivation
cases (`k8s.io/klog/v2` -> `klog`, `k8s.io/api/core/v1` -> `v1`,
`github.com/onsi/ginkgo/v2` -> `ginkgo`, `example.com/foo/v10` -> `foo`). `--stats`
for the alias package: `call sites 11 = stored 6 + skipped 1 + deferred 4 [ok]`
with exact 3, external 1, no_match 2, skipped_conversion 1, deferred_selector 4, no
bare alias-qualified deferrals. `TestGraphResolverVersionBumpStalesFiles` proves
the fingerprint changes with the version and a file graphed at version 1's
fingerprint is stale `symbols changed` while a fresh graph stores version 2's
fingerprint. `TestGraphStalenessDeclaredNameChange` proves the fingerprint covers
declared names: after a file stores a `no_match` for `dep.Thing(1)`, adding a
non-struct `type Thing int` (not a symbol) in the aliased package makes the
resolving file stale (`symbols changed`) and the call a `skipped_conversion`.

Real DB. The version bump makes the whole graphed tree stale:

```
$ go run ./cmd/indexer graph --status
6633 files stale
  reasons (a file may have more than one, so these sum above the total):
    never graphed: 0
    content changed: 0
    symbols changed: 6633
```

Real run on the tracer package and a client-go-importing package (only these
packages were re-graphed; the rest stay stale until ticket 09's whole-repo run):

```
$ go run ./cmd/indexer graph --package k8s.io/kubernetes/pkg/kubelet/envvars --stats
2 files to graph
graphed 2 files, stored 20 edges
edges stored: 20
  resolution exact: 6
  resolution unresolved: 14
  reason external: 14
  reason no_match: 0
call kind func: 6
call kind unknown: 14
skipped_builtin: 12
skipped_conversion: 0
deferred_selector: 0
invariant: call sites 32 = stored 20 + skipped 12 + deferred 0 [ok]

$ go run ./cmd/indexer graph --package k8s.io/kubernetes/pkg/scheduler --stats
5 files to graph
graphed 5 files, stored 518 edges
edges stored: 518
  resolution exact: 282
  resolution unresolved: 236
  reason external: 236
  reason no_match: 0
call kind func: 282
call kind unknown: 236
skipped_builtin: 159
skipped_conversion: 4
deferred_selector: 906
deferred_local: 25
invariant: call sites 1612 = stored 518 + skipped 163 + deferred 931 [ok]
```

Interpretation. The 14 external rows in `pkg/kubelet/envvars` and the 236 in
`pkg/scheduler` are alias-qualified calls to unindexed import paths (standard
library, `k8s.io/klog/v2`, and the excluded generated client-go trees such as
`listers`); a real-package `no_match` count of 0 is reported as 0 (every
alias-qualified `alias.Name` in these two packages either resolved to an indexed
function or hit an unindexed path). 114 of the scheduler's external rows are
`klog.*` (confirmed with `SELECT ... WHERE callee_raw LIKE 'klog.%' GROUP BY
unresolved_reason` -> `external|114`): before the versioned-path alias fix these
were counted `deferred_selector`, so the fix moved 114 call sites out of the
interim counter (scheduler external 122 -> 236, deferred_selector 1020 -> 906).
The alias-qualified part of `deferred_selector` is gone; what remains in
`pkg/scheduler` (906) is receiver-style selector and chained calls, handled by
tickets 05 and 06.

### Known receivers: methods and parameters (S2 ticket 05)

What changed. Each declaration now carries a per-function type environment: the
receiver's written type (`s *Scheduler`) and every parameter's written type
(`i Interface`, `p *pkg.T`, `f func()`), alongside the flat local-scope names.
It is built by `extractDecl` (receiver via `receiverText`, each
`parameter_declaration`'s `type` field against its name list, and the
declaration's own `type_parameters` names) and any name redeclared in the body (a
`:=`, `var`/`const`, range variable, function-literal parameter, type-switch
guard or select receive variable) is removed before resolution, so an inner
declaration shadows the receiver or a parameter. The scope stays flat per
declaration, matching the tracer's flat local-scope simplification: it can only
turn a known name into an unknown one, never invent a type, so it can miss a
resolution but never produce a wrong exact. Only the receiver and parameters are
recorded; locals, fields and constructor return types are ticket 06.

Rule 3. `analyzeQualified` now tries the type environment first: a selector whose
qualifier is a receiver or parameter name with a written type goes to
`analyzeReceiver`, before the local-scope deferral and before the import-alias
lookup (so a parameter still wins as a receiver, and a name not in the type
environment but in local scope stays `deferred_selector` as before). The written
type is reduced to a named type: one leading `*` is stripped (Go auto-addresses,
so a value call finds a pointer-receiver method and vice versa), a bare
identifier is a same-package type only when the package actually declares a type
of that name (the declared-name index), and `alias.Type` resolves through a known
import alias to that package only when it declares the type too; the
declaration's own type parameters are never types; any other form (slice, map,
func, generic instantiation, inline struct, qualified type through an unknown
alias) is not a named type and returns unknown. A method of that name whose
receiver base equals the type in the same indexed package is `exact` kind
`concrete_method`; a known named interface type (in any indexed package) sets no
`call_kind` and stores no row, but is counted `deferred_interface` until Rule 4
lands. No method and not an interface — including a method promoted only from an
embedded struct — stays `deferred_selector`, never guessed. The method index and
the interface set are built once in `NewIndex` from the symbol kinds, keyed by
`(package, receiver base, method)` and `(package, name)`.

Fixture goldens (`cmd/indexer/testdata/graph/methods/methods.go`, real Postgres,
`TestGraphStageMethodGoldens`). Pointer receiver (`(s *Scheduler).Run` called on
`s *Scheduler` -> `(*Scheduler).Run`), value receiver (`(Widget).Tick` -> `(Widget).Tick`),
parameter-typed call (`x *Box` -> `(Box).M`), a method name on two types
(`Box.M` and `Crate.M`, resolved by receiver type), an interface-typed parameter
(`i Interface` -> `deferred_interface`, no row), an embedded struct left unknown
(`Outer` embeds `Inner`; `o.M()` stays `deferred_selector`), a parameter shadowed
by an inner `:=`, a type-switch guard (`switch s := y.(type)`) and a select
receive (`case s := <-ch:`) that shadow a receiver (each call stays
`deferred_selector`, never the outer receiver's method), and a bare type that is
the declaration's own type parameter (`func typeParam[T any](x T) { x.M() }`)
while the package declares a same-named type with that method (stays
`deferred_selector`, never a wrong exact). `--stats` for the package:
`invariant: call sites 12 = stored 6 + skipped 0 + deferred 6 [ok]`, exact 6,
concrete_method 6, deferred_selector 5, deferred_interface 1. The receiver
self-call inside `Run` resolves through the type environment too, so the pointer
receiver is exercised both from a parameter and from the receiver itself.

The existing `alias` golden changed as intended: the parameter named `klog`
(`func callShadowed(klog *fakeKlog)`) still shadows the import alias (Rule 2 does
not fire), but its own written type `*fakeKlog` is now known, so the inner
`klog.V` resolves to `exact` `concrete_method`
(`k8s.io/kubernetes/alias.(fakeKlog).V`) instead of staying deferred; the outer
chained `klog.V(2).InfoS` stays deferred. The alias `--stats` invariant is now
`call sites 11 = stored 7 + skipped 1 + deferred 3 [ok]` (exact 4, func 3,
concrete_method 1, deferred_selector 3). `TestAnalyzeKnownReceiverCrossPackage`
covers a `d *dep.T` parameter resolving to `dep.(T).M` through an import alias
and an interface-typed parameter counted `deferred_interface`.

Resolver version. `graph.ResolverVersion` is bumped 2 -> 3, so every file graphed
before is stale (`symbols changed`); the fingerprint change is covered by the
existing version-bump test.

Real run (dev DB, `--package k8s.io/kubernetes/pkg/scheduler`; the rest of the
tree stays stale until ticket 09):

```
$ go run ./cmd/indexer graph --package k8s.io/kubernetes/pkg/scheduler --stats
5 files to graph
graphed 5 files, stored 654 edges
edges stored: 654
  resolution exact: 418
  resolution unresolved: 236
  reason external: 236
  reason no_match: 0
call kind func: 282
call kind concrete_method: 136
call kind interface_method: 0
call kind unknown: 236
skipped_builtin: 159
skipped_conversion: 4
deferred_selector: 710
deferred_local: 25
deferred_interface: 60
invariant: call sites 1612 = stored 654 + skipped 163 + deferred 795 [ok]
```

The invariant passes. Before this ticket the same package had exact 282 (all
`func`), `deferred_selector` 906 and `deferred_interface` 0; now the 196
receiver/parameter-typed selector sites (906 - 710) split exactly into 136
`concrete_method` and 60 `deferred_interface`. Confirmed in the DB: 136
`call_kind='concrete_method'` rows for the package and 0 rows breaking the
unresolved shape rule (`(resolution='unresolved') <> (callee_id IS NULL)`). The
review fixes (type-switch and select-receive shadowing, type-parameter and
declared-name gating) do not change this package: re-running after forcing the
five files stale (`graphed_symbols_fp = NULL`, since the resolver version stays
3) gives the identical counts above, and a following run prints `0 files to
graph`.

Checks. `make test` green (throwaway database per test, no new skips);
`make ci-local` green (`go vet`, tests, golangci-lint 0 issues, shellcheck,
actionlint).

Ambiguity. Embedded-struct promotion is deliberately unseen: the method index
holds only methods declared on the named type, so `Outer.M` promoted from `Inner`
is not found and the call stays `deferred_selector`. A cross-package receiver
resolves only through a known import alias (`pkg.T`); an unaliased or dot-imported
type has no alias and stays deferred, and a same-package bare type name is looked
up only in the same package and only when the declared-name index knows it as a
type. Builtin type names (including the interfaces `error` and `any`) are not
declared types and not in the interface set, so `err.Error()` is counted
`deferred_selector`, not `deferred_interface`, and stores no wrong edge; no
special case is added for them. A known interface receiver stores no row and sets
no `call_kind`; only the `deferred_interface` counter moves (the `interface_method`
kind is not observable until Rule 4 lands).


