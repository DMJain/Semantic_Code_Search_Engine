# 08: Apply the cap: Rule 4 rows and candidates_capped

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 48, 50-52, 55, 98, 97, 99.

**What to build:** Interface calls are stored. `MaxInterfaceCandidates` is set to the N recorded in D-075. Up to N candidates give one `ambiguous` row per candidate (D-023), kind `interface_method`. More than N candidates give one `unresolved` row with reason `candidates_capped`, no callee and the raw callee text kept. The cap applies to Rule 4 only. This removes `deferred_interface`. Bump the resolver version.

**Blocked by:** 07

**Status:** ready-for-agent

- [ ] Fixture tests: N implementers give N ambiguous rows with identical raw callee text; N+1 give one `candidates_capped` row with a NULL callee
- [ ] The constant equals the N in D-075
- [ ] `deferred_interface` is gone from `--stats` and the code; the invariant passes; resolver version bumped
- [ ] Real run on one interface-heavy package: counts of ambiguous rows and capped rows recorded
- [ ] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [ ] One commit `S2-08: ...` that also sets this ticket's Status to done
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Function values; the final whole-repo run.
