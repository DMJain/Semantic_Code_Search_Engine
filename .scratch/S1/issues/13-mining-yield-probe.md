# 13: Mining-yield probe

Plan: S1-T12 (`docs/plan/sprints/S1-corpus-symbols.md`). Spec user stories: 47, 48, 49, 50, 21.

**What to build:** `eval mine-yield` reports whether enough real merged pull requests exist inside the candidate slice to mine 25 semantic evaluation queries. It searches GitHub in weekly merged-date windows ending at the tag date, halves any window that reaches the 1,000-result cap, reads the search rate limit from the API and waits on it, caches every response by query and window, and counts PRs that changed one to five non-test non-generated files, all inside the candidate slice and existing at the tag. It reports the 12-month yield, the 18-month extension yield and the per-prefix contribution. The read-only token is already configured.

**Blocked by:** 02 (Pin and fetch the Kubernetes tag), 04 (Walk the corpus)

**Status:** ready-for-agent

- [ ] Without a token the command prints `set GITHUB_TOKEN (read-only, public repos) in .env` and exits non-zero before any network call
- [ ] With the token it writes the results file with `yield_12m`, `yield_18m_extension`, `by_prefix` and `windows_at_cap` = 0; a second run makes 0 HTTP calls
- [ ] Fake-GitHub tests cover pagination, window halving at the cap, the rate limit read from the API and waited out on an injected clock (no real sleeps), and the token never appearing in captured logs
- [ ] Both yields are noted in Results
- [ ] One commit `S1-13: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Leakage filtering and query choice (S4); changing the slice (ticket 14).
