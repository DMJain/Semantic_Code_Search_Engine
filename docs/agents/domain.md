# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Before exploring, read these

- **`docs/plan/glossary.md`**: the project glossary. Read it for the vocabulary of the domain.
- **`docs/plan/decisions.md`**: the project decision log (ADR-lite). Read entries that touch the area you're about to work in.

If any of these files don't exist, **proceed silently**. Don't flag their absence; don't suggest creating them upfront. The `/domain-modeling` skill (reached via `/grill-with-docs` and `/improve-codebase-architecture`) appends to them lazily when terms or decisions actually get resolved.

## File structure

Single-context repo (most repos):

```
/
├── docs/plan/glossary.md
├── docs/plan/decisions.md
└── src/
```

Multi-context repo:

```
/
├── docs/plan/glossary.md               ← system-wide glossary
├── docs/plan/decisions.md              ← system-wide decisions
└── src/
    ├── ordering/
    │   ├── docs/plan/glossary.md       ← context-specific glossary
    │   └── docs/plan/decisions.md      ← context-specific decisions
    └── billing/
        ├── docs/plan/glossary.md
        └── docs/plan/decisions.md
```

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `docs/plan/glossary.md`. Don't drift to synonyms the glossary explicitly avoids.

If the concept you need isn't in the glossary yet, that's a signal: either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/domain-modeling`).

## Flag decision-log conflicts

If your output contradicts an existing decision in `docs/plan/decisions.md`, surface it explicitly rather than silently overriding:

> _Contradicts D-0007 (event-sourced orders), but worth reopening because…_
