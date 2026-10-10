# 06: `raw_calls` extraction (S1-T06)

Plan: S1-T06 (`docs/plan/sprints/S1-corpus-symbols.md`)

**What to build:** For each function or method body collect call-expression function-operand text as written (trimmed to 80 characters), deduplicated, source order, at most 40, including calls in function literals; builtins and conversions are dropped by a fixed list.

**Blocked by:** 04

**Status:** ready-for-agent

**Spec user stories:** 17

- [ ] Golden test over a fixture with method chains, builtins and closures
- [ ] `schedulePod` on the real tree has a non-empty list
- [ ] One commit `S1-T06: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Resolving anything (S2); using `raw_calls` outside summary input (D-019).
