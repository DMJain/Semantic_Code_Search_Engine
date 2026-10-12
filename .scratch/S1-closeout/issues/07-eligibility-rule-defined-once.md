# 07: Eligibility rule defined once

Plan: S1 close-out spec, stories 30, 31, 32, 48. Review finding: "in the slice, function or method, body of at least 3 lines" (D-014) is written five times with a bare number (runner eligibility, benchmark skipped-count, audit, gate and the Go predicate).

**What to build:** One definition of an eligible function with a named threshold referencing D-014. The runner, the benchmark skipped-count, the audit and the gate all use it, with the SQL form generated from or tested against the same threshold. No behavior change: the counts reported before and after are identical on the real database.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] The threshold appears in exactly one place in non-test code
- [ ] A test runs the Go form and the SQL form against the same fixtures (boundary: 2, 3, 4 line bodies; function vs method vs struct; in-slice vs out) and they agree
- [ ] Eligible and skipped counts on the real database are the same before and after (trimmed output)
- [ ] Existing tests pass without edits to their assertions
- [ ] Prerequisite: the owner pauses the overnight run before the runner is touched; the agent never signals the run
- [ ] One commit `S1C-07: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Changing the threshold or D-014.
