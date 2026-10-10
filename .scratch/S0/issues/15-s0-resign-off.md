# 15: Re-sign-off S0 (S0 gap)

**What to build:** One recorded run of every verification command after the gap fixes, the S0 sprint Results brought up to date, and the CI decision restated: the push stays deferred (D-061) and `make ci-local` (S1) is the substitute. Then the S1 branch is rebased onto the updated S0 branch.

**Blocked by:** 11, 12, 13, 14

**Status:** done

- [ ] `make reset-db` (empty-guard first), `go vet ./...`, `make lint`, `make test` (0 SKIP, 0 cached), `make doctor` (8 of 8), `doctor --probe-all`, `doctor --ollama-checks` all exit 0; summaries pasted in Results
- [ ] Results list the five gap tickets with evidence links and the deferred CI step
- [ ] `git merge-base --is-ancestor s0-setup-verification s1-corpus-symbols` succeeds after the rebase; S1 plan docs still apply cleanly
- [ ] One commit

Out of scope: pushing, merging.
