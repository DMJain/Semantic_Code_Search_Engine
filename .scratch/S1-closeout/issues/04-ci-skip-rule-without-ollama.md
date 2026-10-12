# 04: CI skip rule holds without a model server

Plan: S1 close-out spec, stories 24, 25, 26, 27, 28. Review findings: the live summarize test skips without `OLLAMA_URL` and breaks the strict no-skip rule in CI; script lint "skipped when absent" handling is missing.

**What to build:** `make test` and `make ci-local` pass with no model server and with zero skipped tests. The live-model test moves behind an explicit opt-in make target that is not part of the default run and that fails (never skips) when the model server is unreachable. The script lint step runs when the tool is installed; when not, one explicit line says "skipped: not installed", and `ci-local` output carries that line so the sign-off Results can quote it.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] `make ci-local` with the model server stopped passes with no `--- SKIP`
- [ ] The opt-in live target fails with a clear message when the server is unreachable, and passes when it is up
- [ ] With the lint tool absent, the "skipped: not installed" line appears in `ci-local` output; with it present, the lint runs
- [ ] The strict skip check itself is unchanged
- [ ] One commit `S1C-04: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Pushing or remote CI (D-069).
