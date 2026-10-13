# 03: Freshness and whole-repo trial run

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 62-73, 75, 78, 100, 97, 99.

**What to build:** `indexer graph` knows which files need work. A file is stale when its content hash differs from the one it was graphed at, or when the symbol-set fingerprint it was graphed against differs from the current one. The fingerprint is a hash over id, kind, package, qualified name and receiver type of all indexed symbols ordered by id, plus the resolver version; line numbers and body hashes are excluded, so a body-only edit invalidates nothing. `parse` never touches graph state. `graph` processes only stale files and prints `N files to graph`; `graph --status` lists stale files and why. A run interrupted with SIGINT and rerun ends with the same tree-sitter edge count as an uninterrupted run. The command works on every indexed file, not only the slice.

One whole-repo run is made at the end as a performance trial (wall time and stats recorded). Its edges are expected to be mostly deferred and will go stale as later tickets bump the resolver version; the final whole-repo run is ticket 09.

**Blocked by:** 02

**Status:** ready-for-agent

- [ ] Staleness tests on the fixture and real Postgres: (i) removing a callee and re-parsing raises no CHECK violation, the edge is gone and the caller's file is reported stale by `--status`; (ii) adding a symbol that makes a `no_match` resolvable makes all files stale; (iii) a body-only edit makes nothing newly stale
- [ ] A test proves the fingerprint changes when the resolver version constant is bumped
- [ ] `graph` then `graph` again prints `0 files to graph`; INT-and-rerun yields the same `select count(*) from edges where source='treesitter'` as an uninterrupted run (integration test on the fixture tree)
- [ ] `graph --status` prints each stale file with the reason (content changed, symbols changed, never graphed)
- [ ] `graph --limit N` processes at most N stale files
- [ ] Whole-repo trial run: all indexed files graphed; wall time and the `--stats` output (including deferred counters) recorded in Results with a note that this run is for performance only
- [ ] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [ ] One commit `S2-03: ...` that also sets this ticket's Status to done
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: New resolution rules, a final complete graph (ticket 09), ground truth.
