# 14: Operational: embed the slice

**What to build:** Produces the vectors every later sprint uses. The pause and resume of the summary run is a human step.

**Blocked by:** 06, 08, 10 (and 07 if activated)

**Status:** done

- [ ] Preconditions recorded in Results: ticket 06 D-080 outcome recorded, ticket 08 passed (`rejected=0`, budget kept or replaced and re-chunked), ticket 07 done if activated.
- [ ] Summary run paused (S1 procedure step 2); `ollama ps` lists no loaded model, pasted into Results; nothing else uses Ollama.
- [ ] `indexer embed` for `chunk_raw` and `chunk_header` for qwen3 on the slice, then `indexer vindex` for both kinds; summary run resumed right after (`make summarize-status` shows it running).
- [ ] `select target_kind, count(*) from embeddings where model='qwen3-embedding:0.6b' group by 1` shows `chunk_raw` and `chunk_header` each equal to the in-slice chunk count minus the `embed_failures` rows of that kind; the failure sets are identical per model; failures at most 1% of in-slice chunks.
- [ ] Results record wall time, items per second, per-batch time, vector counts per `(model, kind)`, index sizes, `too_large` and `truncated` counts, degraded count, and the per-item qwen3 real-versus-estimate ratio (reported only; `ChunkMaxTokens` is not changed).
