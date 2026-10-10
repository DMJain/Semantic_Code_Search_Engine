# 11: `indexer summarize` with resume and sampling

Plan: S1-T10 (`docs/plan/sprints/S1-corpus-symbols.md`). Spec user stories: 42, 43, 44.

**What to build:** `indexer summarize --level function` creates summaries idempotently and resumably: rows move from pending to done or failed with a stored reason, done rows with an unchanged input hash are skipped, and SIGINT or SIGTERM finish the in-flight item and exit 0. Flags select a limit, a concurrency of 1 or 2, retry of failed rows, and a seeded sample stratified across the four slice roots in proportion to eligible counts (candidates ordered by qualified name before the seeded shuffle), so the same seed gives byte-identical id lists.

**Blocked by:** 10 (Summary prompt and request guards), 03 (Schema for symbols and summaries)

**Status:** ready-for-agent

- [ ] `--limit 20` creates 20 done rows; a rerun makes 0 Ollama calls; no duplicate `(level, target, prompt version)` rows
- [ ] A SIGINT mid-run leaves no partial row; resume completes with 0 duplicates; an over-large symbol ends failed with reason `too_large` and no HTTP call
- [ ] `--sample 200 --seed 3` run twice prints byte-identical id lists; unit tests cover determinism and stratification
- [ ] Integration tests against a fake Ollama in throwaway databases; concurrency 2 passes under the race detector
- [ ] One commit `S1-11: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: File and package levels; embedding summaries.
