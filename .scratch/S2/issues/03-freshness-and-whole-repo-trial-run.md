# 03: Freshness and whole-repo trial run

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 62-73, 75, 78, 100, 97, 99.

**What to build:** `indexer graph` knows which files need work. A file is stale when its content hash differs from the one it was graphed at, or when the symbol-set fingerprint it was graphed against differs from the current one. The fingerprint is a hash over id, kind, package, qualified name and receiver type of all indexed symbols ordered by id, plus the resolver version; line numbers and body hashes are excluded, so a body-only edit invalidates nothing. `parse` never touches graph state. `graph` processes only stale files and prints `N files to graph`; `graph --status` lists stale files and why. A run interrupted with SIGINT and rerun ends with the same tree-sitter edge count as an uninterrupted run. The command works on every indexed file, not only the slice.

One whole-repo run is made at the end as a performance trial (wall time and stats recorded). Its edges are expected to be mostly deferred and will go stale as later tickets bump the resolver version; the final whole-repo run is ticket 09.

**Blocked by:** 02

**Status:** done

- [x] Staleness tests on the fixture and real Postgres: (i) removing a callee and re-parsing raises no CHECK violation, the edge is gone and the caller's file is reported stale by `--status`; (ii) adding a symbol that makes a `no_match` resolvable makes all files stale; (iii) a body-only edit makes nothing newly stale
- [x] A test proves the fingerprint changes when the resolver version constant is bumped
- [x] `graph` then `graph` again prints `0 files to graph`; INT-and-rerun yields the same `select count(*) from edges where source='treesitter'` as an uninterrupted run (integration test on the fixture tree)
- [x] `graph --status` prints each stale file with the reason (content changed, symbols changed, never graphed)
- [x] `graph --limit N` processes at most N stale files
- [x] Whole-repo trial run: all indexed files graphed; wall time and the `--stats` output (including deferred counters) recorded in Results with a note that this run is for performance only
- [x] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [x] One commit `S2-03: ...` that also sets this ticket's Status to done
- [x] Report: the commands run (trimmed output) and the commit hash

Out of scope: New resolution rules, a final complete graph (ticket 09), ground truth.

## Report

What changed:

- `cmd/indexer/graph.go`: `graph` loads the selected files (all indexed files, or one package with `--package`) and filters staleness in Go against the fingerprint (`graphed_sha != sha` or `graphed_symbols_fp != $fp`, D-076) — `selectGraphFiles` reads all selected rows and the filtering is an in-memory pass, not a DB-side WHERE — then processes only the stale ones; a stale file's whole package is still extracted so the declared-name index covers every file, but only stale files are written; `--limit N` counts stale files written; added `--status` (per-file reasons, independent per-reason counts that may sum above the total, total, list capped at 50 with `... (N more)`, `0 files stale` when none). SIGINT boundary per file is unchanged.
- `internal/graph/resolve_test.go`: `TestFingerprintVersionAndInputs` (deterministic; changes with the resolver version; ignores `StartLine`; changes on kind/add).
- `cmd/indexer/graph_test.go`: idempotent rerun, interrupt-and-rerun equal count, `--limit` mid-package with whole-package declared index, three staleness cases, `--status` reasons, and parse-leaves-graph-state.
- Docs: this ticket, `docs/plan/sprints/S2-call-graph.md` Results.

Commands (trimmed):

```
$ go test -count=1 ./cmd/indexer/ ./internal/graph/
ok  github.com/DMJain/Semantic_Code_Search_Engine/cmd/indexer
ok  github.com/DMJain/Semantic_Code_Search_Engine/internal/graph

$ make test          # green, no SKIP
$ make ci-local      # vet, test, golangci-lint 0 issues, shellcheck, actionlint: all pass

$ time go run ./cmd/indexer graph --stats   # whole-repo trial, dev DB
6631 files to graph
graphed 6631 files, stored 25075 edges
... deferred_selector: 183929, deferred_local: 3782, deferred_interface: 0
invariant: call sites 246482 = stored 25075 + skipped 33696 + deferred 187711 [ok]
real 49.82s

$ go run ./cmd/indexer graph --status      -> 0 files stale
$ go run ./cmd/indexer graph               -> 0 files to graph
```

Staleness test results: (i) callee removed -> cascade deletes the edge, shape CHECK rows = 0, caller's file reported `symbols changed`, and the removed callee's file both `content changed` and `symbols changed` (independent per-reason counts can exceed the file total); (ii) symbol added -> previously `no_match` identifier now resolves `exact`, both files reported stale (`symbols changed` on the content-unchanged one); (iii) body-only edit -> exactly 1 file stale, `content changed`, `symbols changed: 0`, the other file not listed. Interrupt (limit 1) then rerun = same tree-sitter edge count as an uninterrupted run on the fixture tree.

Ambiguity: the body-only rule is implemented literally (a body edit changes the content hash, so the edited file itself is stale), and the ticket's "makes nothing newly stale" is read as no OTHER file becoming stale (the symbol-set fingerprint is unchanged). Recorded in Results.

Commit hash: recorded by the orchestrator's `S2-03: ...` commit (no git command was run by the agent).

