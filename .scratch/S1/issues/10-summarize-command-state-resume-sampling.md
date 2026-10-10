# 10: `indexer summarize` command: state, resume, flags, sampling (S1-T10)

Plan: S1-T10 (`docs/plan/sprints/S1-corpus-symbols.md`)

**What to build:** Idempotent, resumable stage: `summaries` rows move `pending` to `done` or `failed` (reason stored), skip `done` rows with unchanged `input_hash`, order by symbol id; flags `--level`, `--limit`, `--sample N --seed S` (stratified across the four slice roots in proportion to eligible counts, ordered by `qualified_name` before the seeded shuffle), `--concurrency 1|2`, `--retry-failed`; SIGINT/SIGTERM finish the in-flight item and exit 0.

**Blocked by:** 09, 02

**Status:** ready-for-agent

**Spec user stories:** 27, 28, 25

- [ ] `--limit 20` creates 20 `done` rows; a rerun makes 0 Ollama calls; no duplicate `(level, target_id, prompt_version)`
- [ ] A SIGINT mid-run leaves no partial rows, resume completes, 0 duplicates; an over-large symbol ends `failed` with reason `too_large` and no HTTP call
- [ ] `--sample 200 --seed 3` twice prints byte-identical id lists (unit test for determinism and stratification)
- [ ] Integration tests against a fake Ollama (`internal/testdb`), concurrency 2 under `-race`
- [ ] One commit `S1-T10: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: File and package levels; embedding summaries.
