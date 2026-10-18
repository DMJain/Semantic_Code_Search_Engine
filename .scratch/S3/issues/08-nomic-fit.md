# 08: `cmd/tools nomic-fit`

**What to build:** Before any vector is written, measure that chunk and header sizes fit nomic's real context, with the failure rule fixed in D-078.

**Blocked by:** 06 (and 07 if activated)

**Status:** done

- [ ] Separate step that writes nothing to `embeddings`; flag `--seed` (default 1).
- [ ] Sample: the 200 largest in-slice `chunk_header` documents by estimate plus 200 seeded-random; one document per `/api/embed` request with `truncate:false` and nomic's `num_ctx` 2048; the model is unloaded afterwards (`keep_alive` 0).
- [ ] First verifies the installed Ollama returns `prompt_eval_count` on `/api/embed`; if not, records accept/reject only.
- [ ] Writes `eval/results/bench/nomic-fit.json` with `n`, `accepted`, `rejected`, `ratio_p50`, `ratio_p99`, `ratio_max`, `est_max`, `real_max`, model, `ollama_version`.
- [ ] Failure rule: any rejection, or `ratio_max x RequestBudget > 0.95 x 2048`, sets `RequestBudget = floor(0.95 x 2048 / ratio_max)`, then re-run `indexer chunk` and nomic-fit; a second failure is a STOP, raised and not worked around. Tested against a fake server (rejection, over-limit ratio, second failure).
- [ ] Success check: `go run ./cmd/tools nomic-fit --seed 1` gives `rejected=0` and `ratio_max x 1638 <= 1945` and writes the JSON; sample selection is deterministic under the seed (test).
- [ ] Runs under its own summary-run pause and resume; `ollama ps` before the step shows no loaded model and is pasted into Results; never runs while another job uses Ollama.
- [ ] The D-078 outcome line is filled (ratios, budget kept or replaced).
