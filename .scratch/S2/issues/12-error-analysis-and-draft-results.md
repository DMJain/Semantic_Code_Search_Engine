# 12: Error analysis and draft Results

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 89-96, 100, 97, 99.

**What to build:** Explain where tree-sitter loses. A seeded (seed 11) sample of 30 false positives and 30 false negatives, stratified by kind, is classified by cause: unknown receiver, embedded struct, generic, closure, build tag, dynamic, and any other cause found. Recall is reported per stratum (interface method, function value, plain, external). The fix rule is applied: a fix is attempted only if one cause accounts for more than 20% of errors and the change is at most 150 lines in one area; a triggered fix becomes ticket 13 with before and after reports. Unfixed limitations are recorded honestly. The sprint Results section is drafted (numbers cited from tickets 01 to 11, no new decisions). The owner adds the sign-off line afterwards, as in S1, and the ROADMAP S2 row is flipped to done after that.

**Blocked by:** 11

**Status:** ready-for-agent

- [ ] The cause table sums to 60 samples; recall per stratum is reported
- [ ] The fix-rule verdict is stated (triggered or not, with the cause percentages); if triggered, ticket 13 is created and Status is set accordingly
- [ ] Results in the sprint doc are drafted; `grep -n PENDING docs/plan/decisions.md` shows no S2-related PENDING and the open-questions file has no Q-04 or Q-05 unless ticket 01 left Q-05 open with a reason
- [ ] A line is left for the owner's sign-off (not filled by the agent)
- [ ] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [ ] One commit `S2-12: ...` that also sets this ticket's Status to done
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Any fix (ticket 13); the sign-off itself; the ROADMAP flip.
