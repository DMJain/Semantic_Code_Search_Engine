# 14: [human] Freeze the slice

Plan: S1-T13 (`docs/plan/sprints/S1-corpus-symbols.md`). Spec user stories: 33, 51, 52, 53, 54, 69.

**What to build:** The author decides the final slice. They apply the compute-budget rule (D-005) to the projected summarization run length from the benchmark, apply the drop list in order if it is too big, re-run the walk, check that the 12-month yield plus the 18-month extension yield is at least 30 (changing composition within the budget or widening the window if not), and re-run the file-set check on the final set. A new decision entry "Slice frozen (refs D-005)" is appended; D-005 itself is not edited. The hash is also stored where `make verify-slice` reads it.

**Blocked by:** 08 (Audit walk and parse), 09 (Walk vs go/packages file-set check), 12 (Summarization throughput benchmark), 13 (Mining-yield probe)

**Status:** ready-for-human

- [ ] The entry holds the tag and commit, final size and calculation, eligible-function count, ordered drop list, both yields recomputed on the frozen slice from the cache (0 HTTP calls), the file-set check exit code, and the sha256 of the sorted slice path list (repo-relative paths, slash separators, one per line, LF-terminated, bytewise sort)
- [ ] `make verify-slice` exits 0 and its hash equals the shell recomputation over the same sorted list
- [ ] In-slice eligible functions between 15,000 and 25,000, or the deviation justified in the entry
- [ ] The entry states "FINAL, never changes"; no CI hash pin
- [ ] Report: the entry text, the verify-slice output and the commit hash

Out of scope: Any later slice change (needs an owner-authorized superseding entry).
