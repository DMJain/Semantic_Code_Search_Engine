# 03: Safe `summarize-pause`

Plan: S1 close-out spec, stories 13, 14, 15. Review finding: `pkill -f` inside `sh -c` can match its own shell, make or the keep-awake wrapper, and reports success whenever anything matched.

**What to build:** `make summarize-pause` signals only the real summarize process, identified unambiguously (for example a pid file written by the run, or an exact-match lookup that cannot match the recipe's own command line). It prints the pid it signalled, waits for the process to exit with a timeout, and prints a distinct message when nothing was running or when the process did not exit. The keep-awake wrapper is never signalled; it exits with its child. After exit, no row is left half-written.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] A test with a stand-in long-running process whose command line contains the same text as the recipe: the stand-in is signalled, the recipe's own shell and a wrapper are not
- [ ] "Nothing running" and "did not exit within timeout" are distinct outputs with distinct exit codes
- [ ] The pid file (if used) is removed on clean exit and ignored when stale
- [ ] The agent does not signal the live overnight run while testing; tests use only stand-ins
- [ ] One commit `S1C-03: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Changing resume behavior; supervising the run.
