# 09: Function values and the final whole-repo run

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 44, 59, 62, 75-78, 98, 97, 99.

**What to build:** The tree-sitter graph is complete. A call through a function-typed local, parameter or closure variable gets kind `func_value` and is stored `unresolved` with reason `func_value`; a package-level function variable (known from the declared-name index) is reclassified from `no_match` to `func_value`. `deferred_local` is removed, so no deferred code path remains: from here a fresh graph cannot contain deferred rows. Bump the resolver version.

Then the final whole-repo run: every indexed file graphed, wall time recorded, `--stats` by resolution, reason and kind recorded, `--status` shows 0 stale files. One fixture-run test proves that all five unresolved reasons occur.

**Blocked by:** 08

**Status:** ready-for-agent

- [ ] Fixture goldens: local func variable, func-typed parameter, closure variable, package-level func variable (func_value, not no_match)
- [ ] No `deferred_*` counter exists in `--stats` or the code; the invariant (total = stored + skipped) passes
- [ ] A test over a fixture run asserts every allowed `unresolved_reason` value occurs at least once
- [ ] Final whole-repo run: wall time, `--stats` output by resolution, reason and kind recorded; `graph --status` prints 0 stale files; `graph` then prints `0 files to graph`
- [ ] Resolver version bumped
- [ ] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [ ] One commit `S2-09: ...` that also sets this ticket's Status to done
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Ground truth, the report, error analysis.
