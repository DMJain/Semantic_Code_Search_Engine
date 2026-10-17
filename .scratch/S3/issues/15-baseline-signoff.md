# 15: Baseline sign-off and sprint doc

**What to build:** The S3 baseline is demonstrated on real data and the sprint record is complete.

**Blocked by:** 12, 13, 14

**Status:** ready-for-agent

- [ ] `indexer search --config A1 "preempt lower priority pods"` prints 10 in-slice files in under 1 second; `--config A0` also works.
- [ ] `indexer ann-overlap --model qwen3-embedding:0.6b --kind chunk_header --queries probe` and the same for `chunk_raw`: chunk-level mean at least 95% at the chosen `ef_search`, or `ef_search` tuned to reach it; mean, min, file-level overlap and the chosen width recorded in Results and D-027.
- [ ] `EXPLAIN` of the retrieval query shows the HNSW index used on the real data.
- [ ] `docs/plan/sprints/S3-chunks-embeddings-baseline.md` is rewritten from the spec: 1000/550 ceilings, no-title chunk embeddings, the request budget and nomic-fit, the ticket list, the Results section filled with every measurement named in the spec.
- [ ] ROADMAP row for S3 updated; D-078 and D-080 outcomes filled; the S4 carry-list (DEV `--exact` parity, file-level overlap, `short_results`, `search` and `ef_search` in result JSON) is stated.
- [ ] `make ci-local` passes.
