# 10: `indexer vindex`

**What to build:** Builds the per-model, per-kind HNSW index so the planner can use it.

**Blocked by:** 09

**Status:** ready-for-agent

- [ ] `indexer vindex --model M --kind K [--dim N]` creates `emb_hnsw_<slug>_<kind>_<dim>` with `CREATE INDEX IF NOT EXISTS` as a partial HNSW expression index on the cosine operator class (m=16, ef_construction=128); idempotent.
- [ ] Model and kind literals are validated against the known models and the kinds enum before being inlined; `<dim>` defaults to the registry dimension; prints the index size and the memory setting used.
- [ ] Integration test on a 200-row fixture with the scan setting forced: `EXPLAIN` shows `Index Scan using emb_hnsw_...`, and the same query run ten times through the pool keeps the same plan (the partial-index predicate matches).
- [ ] An invalid model or kind is refused without running any SQL.
