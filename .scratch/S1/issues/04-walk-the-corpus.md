# 04: Walk the corpus

Plan: S1-T03 (`docs/plan/sprints/S1-corpus-symbols.md`). Spec user stories: 15, 16, 17, 18, 19, 20, 21, 6.

**What to build:** `indexer walk` fills the files table from the pinned clone: the named exclusion rules (vendor, generated, protobuf, test data, test files, generated client-go code, build constraint) are applied through one exported function, counted per rule, and the candidate slice is marked. The build context is Linux, amd64, cgo off, set explicitly, so a file that imports C is excluded even when the shell enables cgo. Walking twice reports zero changes and vanished files are removed.

**Blocked by:** 02 (Pin and fetch the Kubernetes tag), 03 (Schema for symbols and summaries)

**Status:** ready-for-agent

- [ ] With the C-toolchain variable set in the environment, the walk's build context still has cgo off, and an `import "C"` file is excluded under the build-constraint rule (environment test)
- [ ] `go run ./cmd/indexer walk` prints per-rule counts, indexed files, slice files and LOC and exits 0; a second run reports `0 changed`
- [ ] The database checks from the sprint file return 0 for vendor, test, generated, protobuf, test-data and other-operating-system files; every in-slice file starts with a slice prefix
- [ ] Unit table covers each exclusion rule with positive and negative paths, including a `_windows.go` file and a file with a Windows build constraint
- [ ] Golden test over a small committed module fixture; idempotency integration test in a throwaway database
- [ ] `make test` 0 SKIP; one commit `S1-04: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Parsing.
