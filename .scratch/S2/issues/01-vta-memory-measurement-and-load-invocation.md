# 01: VTA memory measurement and load invocation

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 1-13, 25, 100, 97, 99.

**What to build:** The ground-truth tool can load the frozen slice, build SSA and the VTA call graph over the slice functions only, and report what that cost, without touching the database. The run records the working load invocation (workspace load; the vendor load is known to fail, D-071), closes Q-04 with a measured peak memory, and closes Q-05 only if the load also works with the network off. This is the only S2 step that can need an owner decision, so it goes first. The SSA/VTA build step it creates is the one ticket 10 extends to store edges; it is not a throwaway.

Before the measurement: stop the model server, pause the summary run with the documented safe pause, run no embedding job and no other session on the machine, and set a memory limit for the Go runtime. State in the Report that this was done.

If peak resident memory is above 16 GiB: write and commit an escalation report (which step ran out of memory, the peak, what was tried), set this ticket's Status to ready-for-human instead of done, and stop. Do not adopt per-root runs, CHA or RTA: they change the ground truth (D-011) and need the owner. Tickets 02 to 09 do not depend on this one and may continue; ticket 10 and later stay blocked until this ticket is resolved.

**Blocked by:** None (can start immediately)

**Status:** done

- [x] Load environment is `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=local` under the workspace load; a unit test covers the environment and the shape of the result metadata
- [x] `go run ./cmd/callgraph-gt --check` still exits 0 and prints `file-set: equal (876 files)` after the load settings change
- [x] A read-only run under `/usr/bin/time -l` with a memory limit exits 0 and writes result metadata containing `peak_rss_bytes`, packages loaded, SSA function count, VTA edge total, in-slice edge count and wall time; `select count(*) from edges` is unchanged by the run
- [x] The VTA input is the slice functions plus the initial call graph only
- [x] A load with `GOPROXY=off` and `GOFLAGS=-mod=readonly` is attempted and its outcome recorded
- [x] D-025's working invocation is filled (no PENDING left on that line); the Q-04 entry is deleted with the measured peak in the Report; the Q-05 entry is deleted only if the offline load succeeded, otherwise it stays with the reason — the offline load FAILED, so Q-05 stays open with the reason (`k8s.io/klog/v2@v2.140.0` not in the module cache)
- [x] If peak > 16 GiB: escalation report committed and Status is ready-for-human (the other criteria are then not required) — not applicable, peak is 5.04 GiB
- [x] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [x] One commit `S2-01: ...` that also sets this ticket's Status to done
- [x] Report: the commands run (trimmed output) and the commit hash

Out of scope: Storing any edge (ticket 10); the report; per-root runs, CHA, RTA or any other fallback; changing the slice.

## Report

Environment for the measurement: model server (Ollama) stopped, `make summarize-pause` reported nothing running (the summary run was not alive; exit 2), no embedding job and no other session on the machine, Go runtime memory limit `GOMEMLIMIT=14GiB`.

Code added: `cmd/callgraph-gt/measure.go` (the `--measure` read-only run) and `cmd/callgraph-gt/measure_test.go`; `cmd/callgraph-gt/main.go` gained the `--measure` and `--out` flags (the existing `--check` is unchanged). The load uses the workspace invocation, the full load mode `NeedName|NeedFiles|NeedSyntax|NeedTypes|NeedTypesInfo|NeedImports|NeedDeps`, `Tests:false`, and an environment ending `CGO_ENABLED=0`. SSA is `ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)` + `prog.Build()`; VTA is seeded with the slice functions only over `cha.CallGraph(prog)`. Load errors are collected across the whole import graph with `packages.Visit` (a dependency error is not visible on the initial packages alone); the slice-file mapping is the shared `inSliceRel` helper, also used by `--check`, so the two sides cannot drift.

Commands run (trimmed):

```
$ make summarize-pause
summarize-pause: nothing running (no process matches '^bin/indexer summarize( |$)')   # exit 2

$ go run ./cmd/callgraph-gt --check
load: workspace (GOWORK=auto, default module resolution)
file-set: equal (876 files)                                                           # exit 0

$ docker compose exec -T postgres psql -U codesearch -d codesearch -tAc 'select count(*) from edges'
0

$ set -a; source .env; set +a
$ GOMEMLIMIT=14GiB /usr/bin/time -l go run ./cmd/callgraph-gt --measure --out eval/results/callgraph/gt-memory.json
peak_rss_bytes: 5407965184
wall_ms:        13242
packages:       332
ssa_functions:  263605
vta_edges:      39616
in_slice_edges: 14469
load:           GOOS=linux GOARCH=amd64 GOWORK=auto GOTOOLCHAIN=local CGO_ENABLED=0 go list -deps ./pkg/scheduler/... ./pkg/kubelet/... ./pkg/controller/...
offline load:   failed: staging/src/k8s.io/apimachinery/pkg/api/resource/amount.go:23:2: k8s.io/klog/v2@v2.140.0: module lookup disabled by GOPROXY=off; ... (one per unresolved import)
       14.33 real        33.02 user         5.78 sys
          5407997952  maximum resident set size                                           # exit 0

$ docker compose exec -T postgres psql -U codesearch -d codesearch -tAc 'select count(*) from edges'
0                                                                                      # unchanged

$ make test
... no --- FAIL and no --- SKIP ...                                                    # exit 0

$ make ci-local
go vet; make test; golangci-lint run (0 issues); shellcheck; actionlint                # exit 0
```

`eval/results/callgraph/gt-memory.json`:

```json
{
  "peak_rss_bytes": 5407965184,
  "wall_ms": 13242,
  "packages_loaded": 332,
  "ssa_functions": 263605,
  "vta_edges": 39616,
  "in_slice_edges": 14469,
  "load_invocation": "GOOS=linux GOARCH=amd64 GOWORK=auto GOTOOLCHAIN=local CGO_ENABLED=0 go list -deps ./pkg/scheduler/... ./pkg/kubelet/... ./pkg/controller/...",
  "offline_load": {
    "attempted": true,
    "succeeded": false,
    "error": "staging/src/k8s.io/apimachinery/pkg/api/resource/amount.go:23:2: k8s.io/klog/v2@v2.140.0: module lookup disabled by GOPROXY=off; ... (one per unresolved import)"
  },
  "gomemlimit": "14GiB"
}
```

Outcomes:

- **Q-04 closed.** Peak resident memory is 5,407,965,184 bytes (5.04 GiB), well under the 16 GiB escalation threshold. No escalation report; no fallback (per-root, CHA, RTA) is adopted. `docs/plan/open-questions.md` Q-04 deleted.
- **Q-05 stays OPEN.** The load with `GOPROXY=off GOFLAGS=-mod=readonly` FAILS (exit 1): `k8s.io/klog/v2@v2.140.0: module lookup disabled by GOPROXY=off` (not in the module cache; first seen at `staging/src/k8s.io/apimachinery/pkg/api/resource/amount.go:23:2`). The workspace load therefore needs the network for that dependency; the Q-05 entry stays with the reason.
- **D-025 filled.** Working invocation (workspace load, refs D-071), inside `data/kubernetes`: `GOOS=linux GOARCH=amd64 GOWORK=auto GOTOOLCHAIN=local CGO_ENABLED=0 go list -deps ./pkg/scheduler/... ./pkg/kubelet/... ./pkg/controller/...` (client-go is dropped from the frozen slice, D-070). The offline attempt fails as above.
- **No database writes.** `select count(*) from edges` is 0 before and after.

Commit: `S2-01: measure VTA memory and record the working load invocation` (hash assigned by the orchestrator; agents do not commit).
