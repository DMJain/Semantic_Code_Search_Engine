# 13: [human] Decide and freeze the slice (S1-T13)

Plan: S1-T13 (`docs/plan/sprints/S1-corpus-symbols.md`)

**What to build:** The author applies the compute-budget rule (D-005) to the projected summarization run length, applies the drop list in order if too big, re-runs `walk`, checks the 12-month plus 18-month extension yield is at least 30 (changing composition within budget or widening the window if not), re-runs `callgraph-gt --check` on the final set, then appends the "Slice frozen (refs D-005)" entry and writes `internal/walk/slice.sha256`. D-005 itself is not edited.

**Blocked by:** 08, 11, 12

**Status:** ready-for-human

**Spec user stories:** 33, 34, 35

- [ ] The new entry holds tag and commit sha, final size and calculation, eligible-function count, ordered drop list, both yields recomputed on the frozen slice from the cache (0 HTTP calls), the `--check` exit code and the sha256 (repo-root-relative paths, `/`, LF-terminated, bytewise sort)
- [ ] `make verify-slice` exits 0 and its hash equals the shell recomputation over the same sorted list
- [ ] In-slice eligible functions between 15,000 and 25,000, or the deviation justified in the entry
- [ ] "FINAL, never changes" stated; no CI hash pin
- [ ] Report: the entry text, the verify-slice output and the commit hash

Out of scope: Later slice changes (forbidden without an owner-authorized superseding entry).
