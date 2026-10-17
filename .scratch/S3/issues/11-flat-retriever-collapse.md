# 11: Flat retriever and collapse helper

**What to build:** A0 and A1 retrieve ranked slice files through one shared collapse helper (D-039, D-084).

**Blocked by:** 10

**Status:** ready-for-agent

- [ ] `Result{File, Score, Source, Evidence}`; `Hit{Kind, ChunkID, File, StartLine, EndLine, Score}`; `Source` is `flat_raw` (A0) or `flat_header` (A1); score is `1 - (vec <=> q)`.
- [ ] The collapse helper applies in order: slice filter, best-score-per-file with all hits kept, stable sort by score descending then path ascending, cut to the limit.
- [ ] Flat retriever in one transaction sets `hnsw.ef_search` to at least `ChunkFetch` = 100, fetches 100 chunks with validated, inlined model and kind literals, then collapses; the query is embedded once.
- [ ] Evidence per file is every fetched hit, ordered by score descending then chunk id ascending.
- [ ] Fewer than k distinct files sets `short_result` with no refetch; the count is reported per config.
- [ ] Tests: two chunks from one file give one result at the best score with both in evidence; ties, empty and limit; the slice filter runs before dedupe and cut (12 hits, the 10 best out of slice, 2 in slice, returns those 2); 15 chunks from 2 files ranked first plus 20 more files return 10 distinct files; `ef_search >= ChunkFetch` is asserted; `short_result` is set when fewer than k files remain; a live query returns at most 10 unique in-slice files.
