# 13: Doctor against a really stopped Ollama (S0 gap)

**What to build:** The "Ollama stopped" acceptance is shown with Ollama actually stopped, not simulated by a closed port. Ollama here runs as a manually started `ollama serve` (not a service), so the stop and the restart procedure are detected first. After the check, Ollama is running again with all three models listed and `make doctor` shows 8 of 8 passing.

**Blocked by:** None (can start immediately). Owner approved stopping and restarting Ollama.

**Status:** done

**Found by:** S0 code review (stop was simulated).

- [ ] Before: process command line and environment-relevant arguments recorded; `ollama list` shows three models
- [ ] With the process stopped: `make doctor` exits non-zero, names `ollama reachable` as failing, prints the fix message, and still reports the Postgres checks; transcript recorded
- [ ] Ollama restarted detached; `ollama list` shows the same three models with the same digests as D-060; `make doctor` 8 of 8, exit 0
- [ ] One commit (Results update)

Out of scope: changing how Ollama is installed or run.
