# 03: Plain `search_tsv` column (S0-T03, decision D-004)

**What to build:** A second, reversible migration replaces the generated full-text column on chunks with a plain tsvector column (not null, default empty) and its GIN index, so a later sprint can fill it with identifier-split tokens from Go. The first migration is not edited. The down migration restores the original generated column and index exactly as in the first migration. Filling the column is a later sprint.

**Blocked by:** 02 (Migrations proven from a fresh volume)

**Status:** ready-for-agent

**Spec user stories:** 9, 10, 11

- [ ] After a fresh reset (tables empty, ticket 02's guard applies), the chunks table description shows `search_tsv | tsvector | not null default ''::tsvector` with no `generated` clause
- [ ] Down once then up once succeeds
- [ ] An integration test inserts a symbol and a chunk with an explicit `search_tsv`, matches it with `@@ to_tsquery('simple','foo')`, and asserts the column's `is_generated` is `NEVER`
- [ ] `make test` shows PASS, no SKIP
- [ ] One commit `S0-T03: ...`; nothing pushed
- [ ] Report: table description excerpt, test output, commit hash

Out of scope: populating the column.
