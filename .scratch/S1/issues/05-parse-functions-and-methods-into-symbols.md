# 05: Parse functions and methods into symbols

Plan: S1-T04 + T07 (core) (`docs/plan/sprints/S1-corpus-symbols.md`). Spec user stories: 22, 23, 24, 28, 29, 30, 31, 32, 33.

**What to build:** `indexer parse` turns every walked file into function and method symbols with qualified names that preserve pointer versus value receivers, signature, line range and doc comment. Duplicate names in a package (several `init` functions, same names in different files) get deterministic `#n` suffixes numbered across the whole package. Parsing is idempotent and resumable after an interrupt. A symbol keeps its id when its file is re-parsed, symbols that vanished from a file are removed together with their function summaries, and summary freshness is decided by the recomputed input hash (D-065). Files with parse errors are stored and counted.

**Blocked by:** 01 (ci-local and CGO-safe CI), 04 (Walk the corpus)

**Status:** ready-for-agent

- [ ] `indexer parse` twice: the second run prints `0 files to parse`; an interrupted run resumed gives the same symbol count as an uninterrupted run; no duplicate qualified name
- [ ] `go test -count=1 ./...` passes; goldens cover pointer and value receivers, generics, multiple returns, variadic, embedded-type methods and doc comments with blank-line gaps; no golden is rewritten by `make test`
- [ ] On the real tree `schedulePod` has the qualified name `k8s.io/kubernetes/pkg/scheduler.(*Scheduler).schedulePod`
- [ ] Integration tests (throwaway database): a symbol's id is unchanged after a re-parse with the marker cleared; a removed function leaves no symbol and no function summary and the orphan query returns 0; an inserted extra `init` makes the shifted summaries report stale; cross-file `#n` numbering for two files with two `init` each
- [ ] `make ci-local` exits 0 (the parser compiles with the C toolchain)
- [ ] One commit `S1-05: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Structs and interfaces, raw calls, edges.
