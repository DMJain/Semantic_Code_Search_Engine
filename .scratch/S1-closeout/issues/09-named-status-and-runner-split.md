# 09: Named status and level values, runner split

Plan: S1 close-out spec, stories 37, 38, 48. Review findings: status and level are raw strings in SQL and Go; the summarize runner carries the run loop, status, benchmark and sampling.

**What to build:** Summary status (done, pending, failed) and level (function, later file and package) become named values defined once and passed as SQL parameters. The runner is split so the run loop, status reporting, benchmark and sampling are separate units that each change for one reason. Pure refactor: row contents, input hashes and the prompt version are unchanged, so existing rows stay valid.

**Blocked by:** 07, 08

**Status:** ready-for-agent

- [ ] No raw status or level string literals remain outside the definition, tests excepted
- [ ] Existing run, status, bench and interrupted-run tests pass without edits to their assertions
- [ ] A query over the real database shows identical row counts by status and the same distinct prompt version before and after
- [ ] Prerequisite: the owner has paused the overnight run; it is resumed by the owner after this and ticket 10 to 12 land
- [ ] One commit `S1C-09: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: New behavior (tickets 10, 11, 12).
