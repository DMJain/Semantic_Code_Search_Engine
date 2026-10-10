# 01: Strict, uncached `make test` (S0-T01)

**What to build:** Running `make test` always executes every test fresh and shows the Postgres integration test as PASS. A missing test-database URL, or any skipped test, makes the command fail loudly instead of passing quietly. The URL comes from the environment file (documented in the environment example and set in the developer's local copy), never hard-coded in the build recipe. The current skip message names the exact variable: `TEST_DATABASE_URL not set; skipping Postgres integration test`.

Also do the process setup if not yet done: create branch `s0-setup-verification` from `main`; commit 1 is the existing plan-doc edits ("docs(plan): mark S0 doing, add Results section"), then a separate commit for `.scratch/S0/`. Append decision **D-061** (process: branch, one commit per ticket, draft PR for CI, owner merges, agents never push to or merge into `main`) to the decision log, append-only.

**Blocked by:** None (can start immediately)

**Status:** done

**Spec user stories:** 1, 2, 3, 4, 40, 42

- [x] `make test` run twice in a row prints no `(cached)` and no `--- SKIP`
- [x] Output shows `--- PASS: TestMigrateCreatesTables` with Postgres up
- [x] With the URL unset, `make test` fails with a clear message; a skipped test also fails the command
- [x] No connection string is hard-coded in the Makefile
- [x] `go vet ./...` and `make lint` report 0 issues
- [x] D-061 recorded; one commit `S0-T01: ...` on the branch; nothing pushed
- [x] Report: both `make test` outputs (trimmed) and the commit hash

Out of scope: race flag, new test targets, CI changes.
