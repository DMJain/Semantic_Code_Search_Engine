# 05: Fresh-walk `callgraph-gt --check`

Plan: S1 close-out spec, stories 20, 21, 22. Review finding: the check compares stored in-slice rows (not a fresh walk) with go/packages, so a stale files table can pass; it still loads the dropped client-go root (D-067, D-070).

**What to build:** The check runs a fresh walk of the pinned tree (same exclusion rules, corpus build context) and compares it with the go/packages file set loaded for the frozen slice's roots only. It prints the symmetric difference (bounded to a readable count) and exits non-zero on any difference.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] A test on the fixture module: a deliberately stale files table does not make the check pass
- [ ] A test: a root outside the slice is not loaded
- [ ] A test: a seeded difference is reported and the exit code is non-zero
- [ ] The check passes on the real frozen slice (trimmed output in the report)
- [ ] The workspace-mode fallback is left as is here; its fate is ticket 06
- [ ] One commit `S1C-05: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Changing the slice; deciding the fallback.
