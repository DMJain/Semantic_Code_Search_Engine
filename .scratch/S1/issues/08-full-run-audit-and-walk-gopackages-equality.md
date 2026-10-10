# 08: Full walk + parse run, audit and walk/go-packages equality (S1-T08)

Plan: S1-T08 (`docs/plan/sprints/S1-corpus-symbols.md`)

**What to build:** Run walk and parse on the pinned tag and record counts; audit 30 seeded symbols (seed 42) against the source lines; add `cmd/callgraph-gt --check` (environment with `CGO_ENABLED=0` appended, `NeedName|NeedFiles`, client-go by import path with `filepath.EvalSymlinks`, shared `walk.Excluded`) and run it on the real tag. Exit 2 means a load error: record it under Q-05 and stop for the owner.

**Blocked by:** 07

**Status:** ready-for-agent

**Spec user stories:** 22, 23, 24, 14

- [ ] Results hold the counts and the 30-sample table with 0 unexplained mismatches; parse error count recorded
- [ ] `go run ./cmd/callgraph-gt --check` exits 0 and prints `file-set: equal (N files)`, or exits 2 with the failure recorded and the owner informed
- [ ] Fixture tests: walk set equals `go/packages` set with `CGO_ENABLED=1` in the environment (including a `_windows.go`, a `//go:build windows` and an `import "C"` file); the environment given to `go/packages` ends with `CGO_ENABLED=0`
- [ ] `vendor/k8s.io/client-go` symlink status at the tag recorded (`ls -l`)
- [ ] One commit `S1-T08: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Parse fixes beyond bugs found; offline loadability and the VTA graph (S2).
