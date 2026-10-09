# Plan guide

Sprint plan for the Kubernetes semantic code search engine (Phase 1) and the installable `lodestone` tool (Phase 2). Nothing here is loaded automatically; sessions read only what is listed below.

## Files
- `ROADMAP.md`: both phases, sprint table, critical path, priority list, the two gates.
- `sprints/S0..S10-*.md` (Phase 1) and `sprints/P2-S0..P2-S10-*.md` (Phase 2): fully specified tasks.
- `architecture.md` (Phase 1) and `v2-architecture.md` (Phase 2), `eval-protocol.md`: canonical design and method; sprint files link to their headings.
- `decisions.md` (IDs `D-nnn`), `glossary.md`, `open-questions.md`.
- Written later by the gates: `phase1-handoff.md`, `phase2-handoff.md`.

## Rules
- Each fact lives in one file; other files link by path and heading.
- Glossary and decisions go to `glossary.md` and `decisions.md` (not `CONTEXT.md` or `docs/adr/`).
- New schema changes are new migrations; never edit an applied one.
- TEST queries are frozen after S4; every configuration runs on TEST exactly once.
- Each task is one ticket, implemented in one fresh agent session, producing one reviewable diff. Split anything bigger.
- No time estimates anywhere: not in plans, specs or tickets.
- Tasks marked `Owner: human` (labeling, grading, recording, installs, releases) are done by the author.

## Per-sprint workflow
1. `grill-with-docs` on the sprint file (settle ambiguities; record decisions in `decisions.md`).
2. `to-spec`: the spec goes to `.scratch/<sprint>/spec.md` (for example `.scratch/S3-chunks-embeddings-baseline/`).
3. `to-tickets`: one ticket per task in `.scratch/<sprint>/issues/NN-slug.md`.
4. `implement`: one ticket per fresh session.
5. `code-review` against the ticket's acceptance criteria.
6. Update the ROADMAP status and the sprint file's Results section.

## What an implementation session reads
Only `ROADMAP.md` + the one sprint file + one ticket, plus the sections of `architecture.md`, `v2-architecture.md` or `eval-protocol.md` that the sprint file links to. Decision IDs are looked up in `decisions.md` on demand.

## Gates
S10 ends Phase 1 and P2-S10 ends Phase 2. At each gate the agent writes the handoff document, prints the stop message, and does nothing else. Phase 2 starts only after the author's explicit approval, recorded in `decisions.md` (D-046); Phase 2 is done only after D-047.
