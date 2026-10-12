# 08: Shared helpers

Plan: S1 close-out spec, stories 29, 33, 34, 35, 36, 39. Review findings: duplicated source-range reading, empty-to-null, command dispatch boilerplate and seeded sampling; sampling assumes four roots; `LoadOllama` only returns `LoadEnv`.

**What to build:** One source-range reader (1-based, inclusive) used by the prompt builder, the gate and the audit; one empty-to-null helper; one seeded-sample helper used by the audit, the gate and the symbol and file samplers; one command-dispatch helper (command table, usage, flag-set creation) shared by the indexer and eval binaries. The sample takes its roots from the slice definition instead of a hard-coded four. The Ollama config alias is removed and callers use the one loader. No behavior change: the same seed produces the same sample before and after.

**Blocked by:** 07

**Status:** ready-for-agent

- [ ] Each helper has a single definition; the duplicates are gone
- [ ] A test pins seed 3 samples (a recorded expected list) so refactoring cannot change which items are drawn
- [ ] A test: the sample's strata come from the slice definition (three roots today)
- [ ] The quality-gate sample sheet for seed 3 is byte-identical before and after
- [ ] Existing tests pass without edits to their assertions
- [ ] One commit `S1C-08: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Moving the gate command (ticket 06).
