# 02: Migration 00003 and slice config (S1-T02)

Plan: S1-T02 (`docs/plan/sprints/S1-corpus-symbols.md`)

**What to build:** Reversible migration adding `symbols.raw_calls`, `files.parsed_sha`, `summaries.reason` and the unique index `summaries_uniq (level, target_id, prompt_version)`; `internal/walk/slice.go` holds the ordered slice prefixes and an empty drop list.

**Blocked by:** 01

**Status:** ready-for-agent

**Spec user stories:** 8, 9

- [ ] `make migrate` then `\d symbols|files|summaries` show the new columns
- [ ] `make migrate-down migrate-up` exits 0 and `\d summaries` is unchanged
- [ ] A second `summaries` row with the same `(level, target_id, prompt_version)` is rejected (integration test using `internal/testdb`)
- [ ] `make test` shows 0 SKIP; `go vet ./...` and `make lint` clean
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Edge columns (S2); any migration after 00003.
