# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase. This is a single-context repo: one glossary and one decision log, both in the plan folder.

## Before exploring, read these

- **`docs/plan/glossary.md`**: the project glossary. Read it for the vocabulary of the domain.
- **`docs/plan/decisions.md`**: the project decision log (ADR-lite, append-only, IDs `D-nnn`). Read the entries that touch the area you're about to work in.
- **`docs/plan/architecture.md`**: the canonical architecture; sprint files link to its headings.

Other plan documents (`docs/plan/ROADMAP.md`, `eval-protocol.md`, `open-questions.md`, `v2-architecture.md`, and the sprint files under `docs/plan/sprints/`) are read on demand when a task names them.

If any of these files don't exist, **proceed silently**. Don't flag their absence; don't suggest creating them upfront. The `/domain-modeling` skill (reached via `/grill-with-docs` and `/improve-codebase-architecture`) appends to the glossary and the decision log lazily when terms or decisions actually get resolved.

## File structure

```
/
├── docs/plan/glossary.md
├── docs/plan/decisions.md
├── docs/plan/architecture.md
├── docs/agents/            ← how skills use the docs and the issue tracker
├── .scratch/<feature>/     ← specs and issues (local markdown tracker)
└── cmd/, internal/
```

There is no `CONTEXT.md` and no `docs/adr/` folder: the plan's glossary and decision log serve those roles (D-017).

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `docs/plan/glossary.md`. Don't drift to synonyms the glossary explicitly avoids.

If the concept you need isn't in the glossary yet, that's a signal: either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/domain-modeling`).

## Decision log rules

The decision log is append-only. An outcome is added under the existing entry as "Outcome (ticket, date, evidence)"; a changed decision becomes a new entry that supersedes the old one, and the old entry is marked superseded.

If your output contradicts an existing decision in `docs/plan/decisions.md`, surface it explicitly rather than silently overriding:

> _Contradicts D-006 (pinned Kubernetes tag), but worth reopening because…_
