# 06: Live probe, `--probe-all` and `--ollama-checks` (S0-T07, decisions D-015 and D-060)

**What to build:** The developer can prove each pulled embedding model works against the real runtime, and learn two facts that the truncation guard depends on.

Precondition (human task, no ticket): Darshan has run `ollama pull nomic-embed-text`, so `ollama list` shows the Qwen3 embedder, nomic-embed-text and the 7B coder model. If nomic-embed-text is missing, STOP and tell Darshan; do not pull it.

Behaviour:
- `indexer doctor --probe-all` probes every pulled registry model on three fixed strings (no NaN or infinity, length equals the registry dimension, L2 norm within the allowed range) and prints one line per model: `model dim norm PASS|FAIL`. An unpulled model prints `SKIP (not pulled)` with the pull command. A wrong dimension FAILs and prints both numbers. Exit is non-zero on any FAIL or when the skipped model is the configured embedding model.
- `indexer doctor --ollama-checks` (a) sends one identical 7B generation request twice and prints `gen_count_1` and `gen_count_2` (prompt token counts), and (b) sends over-length input to each embedding model with truncation disabled and prints `embed_oversized: error` or `ok`.

Record: Ollama model IDs and sizes for the three models, the probe table and the check values in the sprint Results. Append **D-060**: "Ollama model IDs recorded at S0; any ID change before S8 means re-running affected DEV configs (an embedder change means re-embed); never mix IDs within one result." If the second generation count is lower than the first, add an outcome line under D-015 noting the prompt-cache effect.

**Blocked by:** 04 (Verified embedding prefixes), and the human model pull (no ticket)

**Status:** done

**Spec user stories:** 24, 25, 26, 27, 28, 29, 30, 31, 32

- [x] With Ollama up and both embedding models pulled, `go run ./cmd/indexer doctor --probe-all` exits 0 and prints PASS twice
- [x] After `ollama rm nomic-embed-text` it prints SKIP and still exits 0 (the configured model is the Qwen3 one); the model is then re-pulled to restore the state (say so in the report) and probe-all prints PASS twice again
- [x] `doctor --ollama-checks` exits 0 and prints `gen_count_1`, `gen_count_2` and `embed_oversized: error` per model
- [x] Tests against a fake Ollama cover: wrong-dimension vector (FAIL, both numbers), NaN vector (FAIL), good vector (PASS), oversized input error, two counts printed; the live run is guarded by `OLLAMA_URL`
- [x] Model IDs, probe table and check values are in Results; D-060 is recorded
- [x] `make test`, `go vet ./...`, `make lint` clean; one commit `S0-T07: ...`; nothing pushed
- [x] Report: probe table, ollama-checks output, model IDs, commit hash

Out of scope: embedding quality comparison (S8).
