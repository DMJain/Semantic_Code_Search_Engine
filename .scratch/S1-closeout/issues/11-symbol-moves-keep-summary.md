# 11: Symbol moves keep their summary

Plan: S1 close-out spec, stories 18, 19. Review finding: when a symbol moves between files and the old file is parsed first, the symbol and its summary are deleted and the summary is lost.

**What to build:** Parsing writes new and changed symbols first and removes symbols absent from the whole parse afterwards, by stable identity, instead of deleting per file as each file is parsed. A summary follows its symbol; a genuinely deleted symbol's summary is removed with it.

**Blocked by:** 09

**Status:** ready-for-agent

- [ ] A failing test first on a small fixture tree: a symbol moved from file A to file B keeps its summary whether A or B is parsed first
- [ ] A test: a symbol deleted from the tree loses its symbol row and its summary
- [ ] Re-parsing an unchanged tree changes no rows (idempotent)
- [ ] Existing parse tests and the real-tag audit tests pass without edits to their assertions
- [ ] One commit `S1C-11: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Re-parsing the real tag; changing stable-id rules (D-063..D-068).
