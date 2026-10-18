# 09: `indexer embed`

**What to build:** Embeds the slice's chunks in two kinds with no title, idempotently and resumably, wiring the existing registry, normalization and probe (D-079, D-081, D-082).

**Blocked by:** 01, 05

**Status:** done

- [ ] Flags: `--model` (default the configured embedding model), `--kinds chunk_raw,chunk_header`, `--limit`, `--retry-failed`; an unknown model is a startup error; the existing probe runs and aborts on failure.
- [ ] Starts with one transaction deleting orphan vectors of `chunk_raw` and `chunk_header` only and prints `orphans_deleted`; no eval or query code ever deletes. Test: delete one chunk, run embed, `orphans_deleted = 2` and `embeddings LEFT JOIN chunks WHERE chunks.id IS NULL` is 0 rows.
- [ ] Work list per kind, ordered by chunk id: in-slice chunks without a vector for `(kind, id, model)` or with a different `input_hash`; a chunk of a non-slice file is never embedded (`count` of such embeddings is 0).
- [ ] `chunk_raw` document = content; `chunk_header` = header plus content; neither render has a title line; the nomic no-title render of `func evictPod()` still hashes to `7de036e7...`; a header render starts with `search_document: ` then the first header line.
- [ ] `input_hash`: `chunk_raw` = H(model, template, content); `chunk_header` = H(model, template, header, content). Test: a graph edge change re-embeds exactly the affected header vectors and no raw vector.
- [ ] Batches of 32, one transaction each with upsert on the unique key; an item above `RequestBudget` is not sent (the fake server records 0 requests for it) and gets a `too_large` failure; an input-too-long reply records `truncated` and retries that batch's items one by one; a wrong-dimension response is rejected.
- [ ] A failure is skipped only while its stored `input_hash` matches; a stale-hash failure is retried; `--retry-failed` re-attempts matching ones.
- [ ] Failures above 1% of in-slice chunks stop the run and are raised.
- [ ] Rerun inserts 0 rows; the failure sets of `chunk_raw` and `chunk_header` are identical per model (`EXCEPT` both ways returns 0 rows) in the integration test.
- [ ] Guarded live run: `--kinds chunk_raw --limit 100` inserts 100 rows with `vector_dims(vec) = 1024` and `max(abs(vector_norm(vec)-1)) < 0.01`.
