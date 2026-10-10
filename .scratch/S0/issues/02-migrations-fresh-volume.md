# 02: Migrations proven from a fresh volume (S0-T02)

**What to build:** The developer can rebuild the database from an empty volume in one command, roll back one migration step, and roll forward again, and the result is the expected schema. The reset refuses to run if any table other than the migration-version table holds rows. The integration test also runs Up, Down, Up in a throwaway database so rollback never touches dev data. A `migrate-up` command exists as an alias of the existing migrate, since the plan's acceptance names it. The goose binary is not on PATH, so everything goes through the project's own migrate command (add a down mode if missing).

**Blocked by:** 01 (Strict, uncached `make test`)

**Status:** done

**Spec user stories:** 5, 6, 7, 8, 44

- [x] Before any wipe: row counts for every table (including an exact count of `summaries`) were checked; if anything other than the migration-version table is non-empty, work stopped and was reported
- [x] `make reset-db` exits 0
- [x] `make migrate-down migrate-up` exits 0 and the table list is identical before and after
- [x] `psql "$DATABASE_URL" -c '\dt'` lists files, symbols, chunks, edges, summaries, embeddings, llm_cache, traces and the migration-version table; `\dx` shows `vector` and `pg_trgm`
- [x] The integration test covers Up/Down/Up in a throwaway database and shows PASS (no SKIP) in `make test`
- [x] One commit `S0-T02: ...`; nothing pushed
- [x] Report: guard output, `\dt` and `\dx` output, commit hash

Out of scope: new schema (ticket 03).
