# 07: `indexer parse` command with stable symbol ids (S1-T07)

Plan: S1-T07 (`docs/plan/sprints/S1-corpus-symbols.md`)

**What to build:** Stage wiring: parse files whose `parsed_sha` differs from `sha`, compute qualified names per whole package with `#n` suffixes, and per file in one transaction upsert on `qualified_name` returning ids, delete the file's unseen symbols and their function summaries, then set `parsed_sha`. SIGINT finishes the current file and exits 0. Summary freshness is by recomputed `input_hash` (D-064).

**Blocked by:** 05, 06

**Status:** ready-for-agent

**Spec user stories:** 16, 18, 19, 20, 21

- [ ] `indexer parse` twice: second prints `0 files to parse`; an interrupted run then resumed gives the same `count(*)` as an uninterrupted one
- [ ] No duplicate `qualified_name`; in-slice symbols exist
- [ ] Integration tests (`internal/testdb`): a symbol's `id` is unchanged after a re-parse with `parsed_sha` cleared; a removed function leaves no symbol and no summary and the orphan query returns 0; an inserted extra `init` makes `init#1`/`init#2` summaries stale
- [ ] Unit test for the cross-file `#n` suffix (two files, same package, two `init` each)
- [ ] One commit `S1-T07: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Edges (S2).
