# 07: Doctor live pass/fail acceptance (S0-T08)

**What to build:** Evidence that `make doctor` is a reliable health check in both states: everything healthy, and Ollama stopped. In the stopped state it fails with an actionable message while still reporting the Postgres checks.

Procedure:
- State A: Postgres up, Ollama up, all models pulled. Record the transcript.
- State B (stops a local service; approved with these guards): detect the install method first (`brew services list | grep ollama; pgrep -lf ollama`). Homebrew service: `brew services stop ollama` then later `brew services start ollama`. Desktop app: `osascript -e 'quit app "Ollama"'` then `open -a Ollama`. A manually started `ollama serve`: ask Darshan before killing. Confirm `pgrep ollama` prints nothing before running doctor.
- Restart Ollama, wait until `curl -s localhost:11434/api/tags` answers, and re-run doctor to confirm 8/8.
- Paste both transcripts and the confirmation run into the sprint Results. Fix doctor only if a fix is needed.

**Blocked by:** 06 (Live probe, `--probe-all` and `--ollama-checks`)

**Status:** ready-for-agent

**Spec user stories:** 33, 34, 35, 44

- [ ] State A exits 0 with every check PASS: config, postgres, vector, pg_trgm, ollama reachable, summary model, embed model, embedding probe (8/8)
- [ ] State B exits non-zero, names the failing check, prints `start Ollama: ollama serve`, and still reports the Postgres checks
- [ ] A test with a closed fake server asserts the Ollama-unreachable path and its fix message
- [ ] Ollama is restored and confirmed running at the end
- [ ] One commit `S0-T08: ...` (even if only Results changed); nothing pushed
- [ ] Report: both transcripts, install method detected, restart confirmation, commit hash

Out of scope: new doctor checks.
