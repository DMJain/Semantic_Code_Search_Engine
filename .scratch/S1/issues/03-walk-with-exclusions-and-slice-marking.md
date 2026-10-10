# 03: `indexer walk` with exclusions and slice marking (S1-T03)

Plan: S1-T03 (`docs/plan/sprints/S1-corpus-symbols.md`)

**What to build:** Walk the clone with named exclusion predicates (`vendor`, `zz_generated`, `pb_go`, `testdata`, `test_go`, `client_go_generated`, `build_constraint`) exported as one function (`walk.Excluded`), count excluded files per rule, upsert `files` with sha, loc and `in_slice`, delete vanished paths. The build context is Linux, amd64, cgo off, set explicitly.

**Blocked by:** 02

**Status:** ready-for-agent

**Spec user stories:** 10, 11, 12, 13, 14, 5

- [ ] With `CGO_ENABLED=1` in the environment the walk's build context has `CgoEnabled == false`; an `import "C"` file is excluded under `build_constraint` (env test)
- [ ] `go run ./cmd/indexer walk` prints per-rule counts, indexed files, slice files and LOC; a second run reports `0 changed`
- [ ] The psql checks from the sprint file return 0 (no vendor, test, generated, pb, testdata or other-OS files) and `in_slice` files all start with a slice prefix
- [ ] Unit table per predicate (including `_windows.go` and `//go:build windows` negatives), golden test over a fixture tree, idempotency integration test (`internal/testdb`)
- [ ] `make test` 0 SKIP; one commit `S1-T03: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Parsing.
