# 04: tree-sitter setup and function/method extraction (S1-T04)

Plan: S1-T04 (`docs/plan/sprints/S1-corpus-symbols.md`)

**What to build:** Add the tree-sitter Go grammar (CGO, enabled only for build and test by T15) and `parse.File` producing function and method symbols with qualified names that preserve pointer versus value receivers, signature, line range and doc comment.

**Blocked by:** 03, 15

**Status:** ready-for-agent

**Spec user stories:** 15, 16

- [ ] `go test -count=1 ./internal/parse/...` passes; no golden file is rewritten by `make test` (`-update` is manual only)
- [ ] Golden JSON for pointer and value receivers, generics, multiple returns, variadic, embedded-type methods and doc comments with blank-line gaps
- [ ] The qualified name of `schedulePod` on the real tree is `k8s.io/kubernetes/pkg/scheduler.(*Scheduler).schedulePod`
- [ ] `make ci-local` exits 0
- [ ] One commit `S1-T04: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Structs and interfaces (T05), calls (T06), database writes (T07).
