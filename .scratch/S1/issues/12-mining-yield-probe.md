# 12: Mining-yield probe on the candidate slice (S1-T12)

Plan: S1-T12 (`docs/plan/sprints/S1-corpus-symbols.md`)

**What to build:** `eval mine-yield [--window-months 12]` queries GitHub search in weekly merged-date windows (halving any window at the 1,000-result cap), reads the search rate limit from `/rate_limit`, caches responses by `(query, window)` under `eval/candidates/cache/` (gitignored), counts PRs with 1 to 5 changed non-test non-generated files all inside the candidate slice and existing at the tag (using `walk.Excluded`), and writes `eval/results/bench/mine-yield.json` with `yield_12m`, `yield_18m_extension`, `by_prefix`, `windows_at_cap`. Add `GITHUB_TOKEN` to config and `.env.example`.

**Blocked by:** 01, 03 (and the token gate: `test -n "$GITHUB_TOKEN"`)

**Status:** ready-for-agent

**Spec user stories:** 31, 32, 14

- [ ] Unset token: exact message `set GITHUB_TOKEN (read-only, public repos) in .env` and non-zero exit, before any network call
- [ ] With the token, JSON written with `windows_at_cap` = 0; a second run makes 0 HTTP calls
- [ ] Fake-GitHub tests: pagination, cap splitting, rate limit from `/rate_limit` and the wait on an injected clock (no real sleeps), token absent from captured logs
- [ ] One commit `S1-T12: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Leakage filtering and query choice (S4); changing the slice (T13).
