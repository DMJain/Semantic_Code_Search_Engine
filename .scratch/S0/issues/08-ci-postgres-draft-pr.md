# 08: CI Postgres and the first draft PR (S0-T04)

**What to build:** CI runs the migration integration test against a real Postgres-with-vector service on every push and pull request, and fails if any store test is skipped. This ticket also performs the first push and opens a draft pull request so CI runs.

Behaviour:
- The test job gets a Postgres service (pgvector, pg17) with the same credentials as local compose, a `pg_isready` health check, and the test-database URL. Tests run uncached. A step runs the store tests verbosely, with pipefail, and fails if the output contains `SKIP`.
- The stale comments saying the test skips in CI are removed (the workflow comment and the integration-test comment).
- **Before the first push:** show Darshan the `on:` block of the CI workflow (today bare `push:` for all branches, plus `pull_request:`) and the output of `gh auth status`, then wait for a go-ahead. Then push `s0-setup-verification` (never `main`) and open a draft PR with `gh pr create --draft --base main`. Watch with `gh run watch`. If CI fails, fix on the branch with an extra commit and re-push; never merge.

**Blocked by:** 03 (Plain `search_tsv` column)

**Status:** done, except the CI observation (deferred by the owner, D-061); the unchecked boxes below are exactly that

**Spec user stories:** 12, 13, 14, 40, 41, 44

- [ ] Workflow is green on the pushed branch
- [ ] `gh run view <id> --log` shows `--- PASS: TestMigrateCreatesTables` and no `--- SKIP` in the store test step; the matching log lines are pasted
- [ ] The `on:` block and `gh auth status` were shown and approved before the push
- [ ] A draft PR targeting `main` exists; `main` was never pushed to
- [x] One commit `S0-T04: ...` (plus labelled fix commits if needed)
- [ ] Report: run URL and id, PASS log lines, PR URL

Out of scope: Ollama in CI.
