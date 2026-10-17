# 01: Migration 00006 and chunker parameters

**What to build:** The database and constants every later S3 ticket stands on: the schema for embeddings, chunk markers and failures, plus the single model-independent size constants and the token estimate (D-078).

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] Migration 00006 adds `embeddings.input_hash` (not null, default empty), a unique key on `(target_kind, target_id, model)`, `files.chunked_sha`, `chunks.start_line` and `chunks.end_line` (not null, default 0), `chunks.header` (if absent), `chunks.content_hash`, `chunks.header_degraded` smallint not null default 0, and `embed_failures(target_kind, target_id, model, reason, input_hash, created_at)` with primary key on the first three.
- [ ] After migrate, `\d embeddings`, `\d chunks` and `\d embed_failures` show the columns, the unique index and the primary key; inserting a duplicate `(kind, id, model)` fails.
- [ ] `make migrate-down migrate-up` exits 0 and `\d embeddings` is unchanged; the migration integration test is extended for all of it.
- [ ] One place defines `ChunkMaxTokens` = 1000, `GroupMaxTokens` = 550 and the estimate `ceil(bytes/4)`; `RequestBudget` = 0.8 x min(num_ctx over the registry) = 1638 is derived from the registry, not hard-coded.
- [ ] Golden-file test for the estimate; a test that `RequestBudget` equals 1638 for the current registry.
