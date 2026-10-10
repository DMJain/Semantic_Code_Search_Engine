# 07: Raw calls per function

Plan: S1-T06 (`docs/plan/sprints/S1-corpus-symbols.md`). Spec user stories: 26, 27.

**What to build:** Each function and method stores its raw calls: the call-expression text as written (trimmed to 80 characters), deduplicated, in source order, at most 40, including calls inside function literals, with builtins and conversions dropped by a fixed list. Raw calls are summary-prompt input only and never feed the call graph (D-019).

**Blocked by:** 05 (Parse functions and methods into symbols)

**Status:** ready-for-agent

- [ ] Golden test over a fixture with method chains, builtins and closures
- [ ] `schedulePod` on the real tree has a non-empty list
- [ ] `indexer parse` stores the list in the same transaction as the symbol; a re-parse leaves it unchanged
- [ ] One commit `S1-07: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Resolving any call (S2).
