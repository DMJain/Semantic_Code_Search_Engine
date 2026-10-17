# 12: `indexer search --config A0|A1`

**What to build:** The minimal runnable baseline: a natural-language query returns ranked slice files.

**Blocked by:** 11

**Status:** ready-for-agent

- [ ] `indexer search --config A0|A1 [--limit 10] "query"` prints tab-separated `rank`, `score` (four decimals), path, `start-end` and the signature line of the best chunk's first declaration (a grouped chunk uses its first declaration's signature).
- [ ] A0 uses `chunk_raw`, A1 uses `chunk_header`.
- [ ] Exit 0 on success; exit 2 when stored vectors for the config's `(model, kind)` are fewer than in-slice chunks less current-hash failures, printing `have N, want M` on stderr; exit 3 is reserved for the ANN gate and used by no other path.
- [ ] Test: delete one embedding, `search --config A1 "x"` exits 2 with `have N-1, want N`; `--config A0` on a database without `chunk_raw` vectors exits non-zero with a message.
- [ ] Unit test of output formatting with a fake retriever; no flags beyond `--config` and `--limit`.
