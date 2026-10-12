# 06: [human] Decide the unrequested scope

Plan: S1 close-out spec, stories 23, 40, 43, 44. Review finding: four pieces of behavior exist that the S1 spec did not ask for and were never explicitly accepted.

**What to build:** The owner decides each item, and the agent records every decision in a new append-only decision entry and applies the removals. Items and default recommendations:
1. One-off gate command in the production indexer: recommend move to a tools command (or keep with a recorded reason).
2. Workspace-mode fallback in the walk-vs-type-checker check: recommend keep only if needed to load the pinned tag (reference Q-05), otherwise remove.
3. Retry once on rate limiting in the shared GitHub client: recommend keep and document.
4. The standalone ETA bug note: recommend fold into the sprint Results and delete the loose file (done in ticket 13).

**Blocked by:** None (can start immediately)

**Status:** ready-for-human

- [ ] The owner's choice for each of the four items is recorded in one decision entry, with a reason
- [ ] Items decided "remove" or "move" are carried out with tests still green
- [ ] Items decided "keep" are named in the relevant package comment or command help
- [ ] One commit `S1C-06: ...`
- [ ] Report: the decisions and the commit hash

Out of scope: Reworking behavior that is kept.
