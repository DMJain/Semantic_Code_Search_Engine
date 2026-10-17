# 05: Context header, set-based recompute and degradation

**What to build:** Each chunk gets its context header from the call graph, recomputed on every run, and degraded once against the smallest embedding context so A0 and A1 fail on the same set (D-081, D-082).

**Blocked by:** 03, 04

**Status:** ready-for-agent

- [ ] Header text, in order: `package` import path, `file` path, `signature`, up to 5 `calls`, up to 5 `called by`; empty lists print `none`; deterministic.
- [ ] Callees and callers come from `edges` with `source='treesitter'` only, `exact` before `name_only`, `ambiguous` excluded, ties by name, names as short qualified names; unit test of the ranking with mixed resolutions; golden for a symbol with 7 callees printing the top 5.
- [ ] One set-based query recomputes headers for all chunks on every run; header and `content_hash` (hash of header and content) are stored and updated only on a hash change.
- [ ] Integration test: deleting one edge changes `content_hash` for exactly the two affected header chunks; rerun prints `0 files re-split, 0 chunks changed`.
- [ ] Degradation at chunk time against `RequestBudget`: header budget = `RequestBudget - tokens(content) - maxDocPrefixTokens` (title is 0, D-079); order callers rank 5 to 1, then callees rank 5 to 1, then the whole signature line; package and file lines never dropped; `chunks.header_degraded` stored (0 = full).
- [ ] Unit test: budget 0 yields package and file only with `header_degraded > 0`; each stage of the order is exercised.
- [ ] Oversized-statement rule: a part above 1000 tokens counts as embeddable while `tokens(content) <= RequestBudget - maxDocPrefixTokens`, else as `oversized_failed`; the command prints `oversized_statements` and `oversized_failed` separately and the `header_degraded` count.
- [ ] Chunks and headers cover the whole filtered repo.
