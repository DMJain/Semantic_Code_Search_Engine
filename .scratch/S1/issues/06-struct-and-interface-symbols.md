# 06: Struct and interface symbols

Plan: S1-T05 (`docs/plan/sprints/S1-corpus-symbols.md`). Spec user stories: 22, 25.

**What to build:** Struct and interface types become symbols with a header-only signature (at most 200 characters), doc comment and qualified name. Grouped type blocks yield one symbol per type; aliases are skipped; function literals are never symbols.

**Blocked by:** 05 (Parse functions and methods into symbols)

**Status:** ready-for-agent

- [ ] Goldens pass for grouped type blocks, generic types and embedded interfaces
- [ ] `indexer parse --stats` lists non-zero struct and interface counts on the real tree
- [ ] Re-running the idempotency and id-stability tests of ticket 05 still passes
- [ ] One commit `S1-06: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Methods declared on interfaces as separate symbols.
