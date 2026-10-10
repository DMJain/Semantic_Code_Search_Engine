# 03: Plain `search_tsv` column (S0-T03, decision D-004)

**What to build:** A second, reversible migration replaces the generated full-text column on chunks with a plain tsvector column (not null, default empty) and its GIN index, so a later sprint can fill it with identifier-split tokens from Go. The first migration is not edited. The down migration restores the original generated column and index exactly as in the first migration. Filling the column is a later sprint.

**Blocked by:** 02 (Migrations proven from a fresh volume)

**Status:** done

**Spec user stories:** 9, 10, 11

- [x] After a fresh reset (tables empty, ticket 02's guard applies), the chunks table description shows `search_tsv | tsvector | not null default ''::tsvector` with no `generated` clause
- [x] Down once then up once succeeds
- [x] An integration test inserts a symbol and a chunk with an explicit `search_tsv`, matches it with `@@ to_tsquery('simple','foo')`, and asserts the column's `is_generated` is `NEVER`
- [x] `make test` shows PASS, no SKIP
- [x] One commit `S0-T03: ...`; nothing pushed
- [x] Report: table description excerpt, test output, commit hash

Out of scope: populating the column.
