# 14: Domain-doc cleanup (S0 gap)

**What to build:** The domain-docs guide describes this repository's real layout (single context: the plan glossary and decision log), with no leftover multi-context template example, and nothing in the repo points at the deleted placeholder architecture doc or the deleted empty query file.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

**Found by:** S0 code review (domain guide still the generic template).

- [ ] The guide names the glossary, decision log and the architecture doc, and the multi-context example is gone
- [ ] A repo-wide search finds no reference to the deleted placeholder architecture doc or the deleted query file (excluding the two real query files and the scratch folder)
- [ ] The no-auto-import check recorded in D-017 still holds (no `@` imports in the agent instructions)
- [ ] One commit

Out of scope: changing the agent instruction file beyond keeping the domain-docs line accurate.
