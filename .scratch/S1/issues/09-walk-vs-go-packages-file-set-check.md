# 09: Walk vs `go/packages` file-set check

Plan: S1-T08 (second half) (`docs/plan/sprints/S1-corpus-symbols.md`). Spec user stories: 7, 21, 35, 36, 37.

**What to build:** `callgraph-gt --check` proves that the files the walk selected for the four slice roots are exactly the files `go/packages` loads under the identical build context (D-067): cgo off appended last to the environment, client-go loaded by import path from the root module, symlinks normalized, the shared exclusion predicate applied. Exit 0 means equal; 1 means unequal with both difference lists capped at 50 lines and totals; 2 means a load error, which is recorded under the open question and stops for the owner.

**Blocked by:** 04 (Walk the corpus)

**Status:** ready-for-agent

- [ ] On a small committed module fixture (with a `_windows.go` file, a Windows build-constraint file and an `import "C"` file) the walk set equals the `go/packages` set, run with the C-toolchain variable set in the environment
- [ ] The environment given to `go/packages` ends with cgo off whatever the process environment holds
- [ ] `go run ./cmd/callgraph-gt --check` on the real tag exits 0 and prints `file-set: equal (N files)`, or exits 2 with the exact failure recorded in the open-questions Q-05 note and the owner informed
- [ ] Whether `vendor/k8s.io/client-go` is a symlink at the tag is recorded
- [ ] Exclusion counts from both sides are printed
- [ ] One commit `S1-09: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Offline loadability and the VTA graph (S2).
