# 05: Docs housekeeping (S0-T09, decisions D-017 and D-032)

**What to build:** The project instruction file and the domain-docs guide point at the plan's glossary and decision log, so there is one source of truth. The placeholder architecture doc and the empty eval query file are deleted and any README links to them are fixed. A check confirms the project instruction files contain no auto-loading `@` imports (there is no `CLAUDE.md` today, only `AGENTS.md`), and the result is recorded under D-017. The issue-tracker guide is untouched. No root context file and no ADR folder are created.

The instruction file's "Domain docs" line becomes: ``Domain docs: glossary `docs/plan/glossary.md`, decisions `docs/plan/decisions.md`. See `docs/agents/domain.md`.`` No other planning content goes into these files. In the domain-docs guide, replace the context-file and ADR-folder references with the plan glossary and decision log; adjust the two file-tree blocks minimally so they stay valid.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

**Spec user stories:** 36, 37, 38

- [ ] `grep -rn "@" AGENTS.md | grep -v "@latest"` shows no import lines (include `CLAUDE.md` only if it exists)
- [ ] `grep -rn "CONTEXT.md\|docs/adr" docs/agents/` and `grep -n "CONTEXT.md\|docs/adr" AGENTS.md` return nothing
- [ ] `test ! -e docs/architecture.md && test ! -e eval/queries.jsonl`
- [ ] `grep -rn "docs/architecture.md\|queries.jsonl" --include=*.md --include=*.go --include=Makefile --exclude-dir=plan --exclude-dir=.scratch --exclude-dir=.git .` returns nothing
- [ ] `go build ./...` passes; D-017 outcome recorded
- [ ] One commit `S0-T09: ...`; nothing pushed
- [ ] Report: grep outputs, diff stat, commit hash

Out of scope: the final README, any other docs rewrite.
