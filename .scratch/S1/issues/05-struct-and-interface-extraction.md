# 05: Struct and interface extraction (S1-T05)

Plan: S1-T05 (`docs/plan/sprints/S1-corpus-symbols.md`)

**What to build:** Extract `struct` and `interface` type specs as symbols (header-only signature, at most 200 characters; grouped `type (...)` blocks yield one symbol per spec; aliases and other types are skipped).

**Blocked by:** 04

**Status:** ready-for-agent

**Spec user stories:** 15

- [ ] Golden test passes for grouped blocks, generic types and embedded interfaces
- [ ] After T07, `indexer parse --stats` lists non-zero struct and interface counts
- [ ] One commit `S1-T05: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Methods on interfaces as separate symbols.
