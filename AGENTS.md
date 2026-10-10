# AGENTS.md

## Agent skills

### Issue tracker

Issues live as local markdown files under `.scratch/<feature>/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Domain docs: glossary `docs/plan/glossary.md`, decisions `docs/plan/decisions.md`. See `docs/agents/domain.md`.

## Working rules

Work directly on `main` with local commits (one commit per ticket). Do not create feature branches. Never push, open pull requests or upload anything to a remote; CI is checked locally with `make ci-local` (D-069).
