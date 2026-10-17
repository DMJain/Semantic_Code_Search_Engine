# 13: `indexer ann-overlap`

**What to build:** Smoke gate that the HNSW index agrees with exact search (D-027).

**Blocked by:** 11

**Status:** ready-for-agent

- [ ] `indexer ann-overlap --model M --kind K --queries <file|probe> [--ef 200,400,800]` (maximum 1000).
- [ ] `probe` builds 30 queries (seed 5) from first sentences of doc comments of random in-slice functions, at least 12 words each; documented in D-027 as a smoke gate that is optimistic (queries sit near their own chunk).
- [ ] For each query compares the HNSW top 10 with exact brute force (index disabled) under the same slice filter, both with a 200-row fetch and `hnsw.ef_search` at least 200; reports mean and min at chunk level (the gate) and at file level after the collapse helper (reported, not gated).
- [ ] A chunk-level mean below 0.95 exits with status 3 and prints `tune ef_search`; `--ef 200,400,800` prints one row per width.
- [ ] Unit test of the overlap computation; guarded live run.
