# 06: Chunk metrics and the whole-repo run (D-080 gate)

**What to build:** The chunk command reports everything the sprint must record, and the whole-repo run is recorded in Results with the D-080 decision.

**Blocked by:** 05

**Status:** ready-for-agent

- [ ] `indexer chunk` prints: chunks total (in slice and outside), files re-split, chunks changed, parts above one, `oversized_statements`, `oversized_failed`, `header_degraded`, token p50/p99/max, `chunks_with_block_comment_above`, `chunks_with_marker_lines` (count and fraction), and wall time.
- [ ] Prints `zero_chunk_files_total`, `zero_chunk_files_with_decls` (at least one top-level declaration, no chunk) and `zero_chunk_files_no_decls`, each also as a fraction of the 876 slice files.
- [ ] The measurement never reads the mined query file sets (TEST pool); a test or review check confirms it.
- [ ] Real run on the full filtered repo recorded in the sprint Results (command, trimmed output, counts); the S1 secrets/scale checks of the repo still pass.
- [ ] Results state the D-080 outcome: whether `zero_chunk_files_with_decls >= 18`; if yes, ticket 07 is activated and no later embedding ticket starts before it is done; if no, ticket 07 is closed as not needed with the number recorded.
- [ ] Results state that B0 indexes whole files (architecture.md keyword baseline) and so can return zero-chunk files that A0 and A1 cannot.
- [ ] D-080 outcome line filled in `decisions.md`.
