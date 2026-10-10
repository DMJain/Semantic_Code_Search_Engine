# 03: Schema for symbols and summaries

Plan: S1-T02 (`docs/plan/sprints/S1-corpus-symbols.md`). Spec user stories: 13, 14, 64.

**What to build:** One reversible migration gives the database the columns and rule the corpus stages need: raw calls on symbols, a parse marker on files, a failure reason on summaries, and uniqueness of a summary per level, target and prompt version. The ordered list of the four slice prefixes and an empty drop list exist as the slice definition. This is the prefactor the parse and summarize tickets build on.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] After `make migrate` the three new columns exist; `make migrate-down` then `make migrate-up` exits 0 and the summaries table is unchanged
- [ ] The database rejects a second summary row with the same level, target and prompt version (integration test in a throwaway database)
- [ ] The slice definition lists the four prefixes and an empty drop list
- [ ] `make test` shows 0 SKIP; `go vet ./...` and `make lint` clean
- [ ] One commit `S1-03: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Edge columns (S2); any later migration.
