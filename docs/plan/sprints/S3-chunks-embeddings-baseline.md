# S3 - Chunks, headers, embeddings, baseline search

## Goal
Split the whole filtered repo into chunks (functions and methods, structs and interfaces, parts of long functions, a group chunk for a type with its same-file constructor), attach a context header built from the call graph and identifier-split lexical text; measure that the sizes fit nomic's real context before any vector is written; embed the slice's chunks in two kinds with no title; build per-model, per-kind HNSW indexes; and ship `indexer search --config A0|A1` with the ANN overlap smoke gate. After S3 the owner can run a natural-language query against A0 or A1, read the zero-chunk, degraded-header and failure counts, and trust that A1 minus A0 isolates the context header.

## Entry criteria
S1 and S2 done (symbols, edges). Function-summary run in progress or finished ([S1 overnight procedure](S1-corpus-symbols.md#overnight-run-procedure)). The existing prefix registry, L2 normalization and probe are NOT rebuilt here. The chunk ceiling and request budget are decided in D-078.

## Links
[architecture.md#stage-chunk](../architecture.md#stage-chunk), [#stage-embed](../architecture.md#stage-embed), [#embedding-variants](../architecture.md#embedding-variants), [#vector-indexes](../architecture.md#vector-indexes), [#result-contract](../architecture.md#result-contract), [#ollama-requests](../architecture.md#ollama-requests), [#token-estimate](../architecture.md#token-estimate); [eval-protocol.md#ann-overlap](../eval-protocol.md#ann-overlap). Decisions: D-003, D-015, D-027, D-028, D-029, D-039, D-062, D-078-D-086.

## Run coordination (summary run vs embedding)
Both jobs use Ollama on one GPU. The summary run keeps running while building S3 code. Pause it ([S1 procedure](S1-corpus-symbols.md#overnight-run-procedure), step 2) before `cmd/tools nomic-fit` and before the embedding run, each with its own pause and resume; before each, `ollama ps` shows no loaded model and the output goes in Results; `nomic-fit` unloads its model; resume follows each step. Embeddings cover the slice only (D-029). The embedding run does not start until `nomic-fit` passes and the D-080 outcome is recorded. The embedding run logs the qwen3 per-item real-versus-estimate ratio and reports it without changing `ChunkMaxTokens`.

## Tickets

| # | Ticket | One-line outcome |
|---|---|---|
| 01 | Migration 00006 and chunker parameters | Migration 00006 plus one model-independent set of constants: `ChunkMaxTokens` 1000, `GroupMaxTokens` 550, the bytes/4 estimate and `RequestBudget` derived from the registry (D-078). |
| 02 | Tracer: `indexer chunk` for plain functions and methods | Every function and method of the whole filtered repo becomes one chunk with the S1 doc comment, exact line spans and lexical text, idempotently and with stable ids. |
| 03 | Parts: split long functions, oversized statements | A function over `ChunkMaxTokens` splits at top-level statement boundaries into parts that each keep the signature; a single oversized statement stays whole and is counted (D-078). |
| 04 | Struct, interface and group chunks | Every struct and interface gets its own chunk; a type with a same-file constructor becomes one group chunk only when the pair fits `GroupMaxTokens` 550 (D-078). |
| 05 | Context header, set-based recompute and degradation | Each chunk gets its context header from tree-sitter edges, recomputed every run by one set-based query and degraded once at chunk time against `RequestBudget`, so A0 and A1 fail on the same set (D-081, D-082). |
| 06 | Chunk metrics and the whole-repo run (D-080 gate) | `indexer chunk` reports every metric and the whole-repo run is recorded; the D-080 outcome is recorded. |
| 07 | Residual chunk (conditional on D-080 trigger) | Activated by D-080: one residual chunk per file for its uncovered top-level const/var/type declarations drives `zero_chunk_files_with_decls` to 0 (D-085). |
| 08 | `cmd/tools nomic-fit` | Measures chunk and header sizes against nomic's real tokenizer with the pre-committed failure rule and writes nothing to `embeddings` (D-078). |
| 09 | `indexer embed` | Embeds the slice's chunks in `chunk_raw` and `chunk_header` with no title, idempotently and resumably, wiring the existing registry, normalization and probe (D-079, D-081, D-082). |
| 10 | `indexer vindex` | Builds the per-model, per-kind HNSW index idempotently with validated literals and prints its size. |
| 11 | Flat retriever and collapse helper | A0 and A1 retrieve ranked slice files through the shared collapse helper (slice filter, best-score-per-file, sort, top-k) with `short_result` (D-039, D-084, D-086). |
| 12 | `indexer search --config A0\|A1` | Prints rank, score, path, line range and the signature line; exit 2 when stored vectors are short, exit 3 reserved for the overlap gate. |
| 13 | `indexer ann-overlap` | Compares HNSW with exact search on 30 seeded doc-comment queries; chunk-level overlap@10 at 95% is the gate, file-level reported (D-027). |
| 14 | Operational: embed the slice | `indexer embed` and `indexer vindex` run on the slice for qwen3 in both kinds; wall time, items/s, vector counts, index sizes and the qwen3 ratio are recorded. |
| 15 | Baseline sign-off and sprint doc | `search` returns 10 in-slice files under 1s, the overlap gate is cleared and this sprint doc and ROADMAP are current. |

## Measurements to record
In this file's Results and indexing metrics ([eval-protocol.md#indexing-metrics](../eval-protocol.md#indexing-metrics)):
- chunks total/slice, parts above one, token p50/p99/max, `oversized_statements`, `oversized_failed`, `header_degraded`, the zero-chunk figures and the marker and block-comment counts.
- Wall time for chunk; embed items/s and per-batch time; vector counts per `(model, kind)`; index sizes; `too_large`/`truncated` counts; the qwen3 per-item real-versus-estimate ratio.
- ANN overlap@10 (mean, min) on the probe set for qwen3 and the `ef_search` chosen.

## Exit criteria / Definition of Done
Chunks and `search_tsv` populated (none empty); A0 and A1 searchable via CLI; HNSW indexes used by the planner; overlap >= 95% or `ef_search` tuned to reach it; summary run resumed or finished; ROADMAP updated.

## Risks + mitigations
- Embedding the slice competes with the summary run for the GPU: the pause rule in Run coordination covers it.
- Index build memory: build after loading; the `maintenance_work_mem` bump is documented in the command output.
- A header or chunk over the request budget: the pre-send check skips and counts `too_large` items; failures above 1% of in-slice chunks STOP the run and are raised, with no silent ceiling change (D-082).

## Demo check
`indexer search --config A1 "<natural-language question>"` returns ranked files in under 1s; `indexer ann-overlap --queries probe` prints overlap >= 95%.

## Results

### Chunk run over the whole filtered repo (S3-T06, 2026-10-18)

Command: `./bin/indexer chunk` (KUBE_DIR `./data/kubernetes` at `v1.37.0`, dev database). First run after `TRUNCATE chunks; UPDATE files SET chunked_sha = NULL`:

```
6633 files to chunk
6633 files re-split, 54938 chunks changed
parts above one: 915
oversized_statements: 349
oversized_failed: 114
header_degraded: 132
chunks: 54938 (9978 in slice, 44960 outside)
tokens p50/p99/max: 91/978/78057
chunks_with_marker_lines: 2483 (4.52% of 54938 chunks)
chunks_with_block_comment_above: 11 (0.02% of 54938 chunks)
zero_chunk_files_total: 150 (17.12% of 876 slice files)
zero_chunk_files_with_decls: 46 (5.25% of 876 slice files)
zero_chunk_files_no_decls: 104 (11.87% of 876 slice files)
wall time: 44.294219209s
```

A second run prints `0 files to chunk` / `0 files re-split, 0 chunks changed` with the same table-wide figures (idempotent). Cross-checks against the database matched the printed lines: `select count(*) from chunks where token_count > 1000` = 349, `... > 1633` = 114, `... header_degraded > 0` = 132.

**D-080 outcome: TRIGGERED.** `zero_chunk_files_with_decls = 46` is at least the threshold of 18, so the residual-chunk ticket (S3-07) is activated and must be done before the embedding run; it must drive `zero_chunk_files_with_decls` to 0. Recorded in `decisions.md` D-080.

**B0 note.** B0 indexes whole-file text (`architecture.md#keyword-baseline-b0`), so it can return zero-chunk slice files that A0 and A1 cannot; a result-count difference between B0 and A0/A1 on those files is a coverage effect, not a retrieval-quality effect.

### Residual chunk run (S3-07, 2026-10-18)

The D-080 trigger fired, so the chunker now gives every file that produced no chunk but has top-level const/var/type declarations one residual chunk (D-085). `ChunkerVersion` was bumped to 4, so the run re-splits every file. Command `./bin/indexer chunk`:

```
6633 files to chunk
6633 files re-split, 295 chunks changed
parts above one: 940
oversized_statements: 410
oversized_failed: 152
header_degraded: 170
chunks: 55233 (10026 in slice, 45207 outside)
tokens p50/p99/max: 92/986/78057
chunks_with_marker_lines: 2485 (4.50% of 55233 chunks)
chunks_with_block_comment_above: 11 (0.02% of 55233 chunks)
zero_chunk_files_total: 104 (11.87% of 876 slice files)
zero_chunk_files_with_decls: 0 (0.00% of 876 slice files)
zero_chunk_files_no_decls: 104 (11.87% of 876 slice files)
wall time: 40.041107334s
```

`zero_chunk_files_with_decls` dropped from 46 to **0**: the 46 slice files (and their 249 out-of-slice peers) now have a residual chunk. The only zero-chunk files left are the 104 doc-only files that hold no declaration and so can never be chunked. Cross-checks matched: 410 chunks over 1000 tokens, 152 over 1633, 170 degraded, 270 synthetic `decl` owner symbols holding 295 residual chunks. This run supersedes the S3-06 chunk figures above (the corpus grew by 295 residual chunks and the counts moved); the S3-06 numbers are kept as the pre-residual snapshot.

The idempotency, stable-id, re-parse-survival and graph-freshness properties of residual chunks are covered by the chunk, parse, walk and graph tests.

### Nomic fit check and the D-078 budget outcome (S3-08, 2026-10-18)

Live Ollama 0.40.1, model `nomic-embed-text`, 400 documents one per request, `truncate:false`, `num_ctx` 2048; the model was unloaded after each run (`ollama ps` was empty before it).

1. First run (`go run ./cmd/tools nomic-fit --seed 1`, budget 1638), with the original sampler that took the 200 largest in-slice `chunk_header` documents from ALL in-slice chunks: `n=400 accepted=386 rejected=14 ratio_p50=1.3130 ratio_p99=1.6837 ratio_max=1.7065 est_max=7283 real_max=2019`; the rule proposed `RequestBudget 1638 -> 1140`. **Bug found:** the pool included in-slice chunks the embed stage skips as `too_large` (content over `EmbeddableContentMax`), which are never sent to Ollama, so 11 of the 14 rejections measured requests that never happen and the check could not converge on a re-run.
2. Sampler corrected to measure only the documents the embed stage will send (content within `EmbeddableContentMax`). Second run at budget 1638: `n=400 accepted=397 rejected=3 ratio_p50=1.3157 ratio_p99=1.7143 ratio_max=1.7941 est_max=1638 real_max=2019`; the rule proposed `RequestBudget 1638 -> 1084`. This is a genuine failure: nomic's real tokenizer produces up to 1.7941x the bytes/4 estimate on embeddable documents, so a 1638-estimate request overflows its 2048 context.
3. `RequestBudget` replaced with `floor(0.95 x 2048 / 1.7941) = 1084` (`EmbeddableContentMax` 1079). The whole-repo `indexer chunk` was re-run (headers re-degraded; no file re-split because the budget is not part of `chunked_sha`), then the fit check passed: `rejected=0 ratio_max=1.7451`, and `1.7451 x 1084 = 1892 <= 1945`. `eval/results/bench/nomic-fit.json` holds this passing run.

**D-078 outcome: budget REPLACED, 1638 -> 1084.** The settled whole-repo chunk run at 1084: `chunks 55233 (10026 in slice)`, `oversized_statements 410`, `oversized_failed 354`, `header_degraded 577`, `tokens p50/p99/max 92/986/78057`, zero-chunk figures unchanged (`0` with decls, `104` doc-only). In the slice: 10026 chunks, 45 over 1079 tokens (the embedding failures, identical in A0 and A1), 127 with a degraded header. These figures supersede the pre-fit numbers in the two sections above; the final sprint numbers are the post-1084 ones.

This resolves the S3-06/S3-07 snapshot drift: any chunk run before the budget replacement used a 1638 budget and is superseded.

### Whole-repo chunk run at the 1084 budget (S3-08 re-run, 2026-10-18)

Command `./bin/indexer chunk` (KUBE_DIR `./data/kubernetes` at `v1.37.0`, dev database). This is the authoritative post-budget-1084 run:

```
55233 chunks (10026 in slice, 45207 outside)
oversized_statements: 410
oversized_failed: 354
header_degraded: 577
tokens p50/p99/max: 92/986/78057
chunks_with_marker_lines: 2485 (4.50%)
chunks_with_block_comment_above: 11 (0.02%)
zero_chunk_files_total: 104 (11.87% of 876 slice files)
zero_chunk_files_with_decls: 0 (0.00% of 876 slice files)
zero_chunk_files_no_decls: 104 (11.87% of 876 slice files)
```

Parts split (table-wide, from the full re-split): `parts above one` 940; the full re-split wall time was 40.0 s. The budget replacement does not re-split files (the budget is not part of `chunked_sha`), so the authoritative pass re-degraded headers in place and took 14.5 s; an idle rerun prints `0 files re-split, 0 chunks changed`.

In the slice: 10026 chunks, 45 over 1079 tokens (the embedding failures, identical in A0 and A1), 127 with a degraded header.

### Run coordination (S3-14, 2026-10-18)

The function-summary run was already COMPLETE (`make summarize-status`: pending 0 done 6694 failed 0), so there was nothing to pause or resume. `ollama ps` showed no loaded model before the fit check and before the embedding run; each fit run unloads its model.

### Embedding run (S3-14, 2026-10-18)

Command `./bin/indexer embed` (default model `qwen3-embedding:0.6b`, kinds `chunk_raw,chunk_header`):

```
orphans_deleted: 0
chunk_raw: embedded 9981, too_large 45, truncated 0
chunk_header: embedded 9981, too_large 45, truncated 0
```

Idempotent rerun: `embedded 0`. The failure sets of `chunk_raw` and `chunk_header` are identical per model (symmetric difference 0 rows). Failures 45/10026 = 0.45% (< 1%). Stored vectors have dimension 1024 and max |norm-1| = 3.0e-8. The run was interrupted once by a shell time cap and resumed; a clean timed sample measured 319 items in 39.2 s (8.1 items/s, 10 batches of 32, ~3.9 s/batch). The qwen3 per-item real-versus-estimate ratio, measured on the same embeddable sample (400 documents, one per request): p50 1.0233, p99 1.2333, ratio_max 1.3163, est_max 1097, real_max 1233, accepted 400/400; recorded in `eval/results/bench/qwen3-fit.json`; reported only, `ChunkMaxTokens` unchanged.

### Vector indexes (S3-14, 2026-10-18)

`./bin/indexer vindex --model qwen3-embedding:0.6b --kind chunk_raw` built `emb_hnsw_qwen3_embedding_0_6b_chunk_raw_1024` (81076224 bytes); `--kind chunk_header` built `emb_hnsw_qwen3_embedding_0_6b_chunk_header_1024` (81772544 bytes). `maintenance_work_mem` 256MB.

### Baseline sign-off (S3-15, 2026-10-18)

`./bin/indexer search --config A1 "preempt lower priority pods"` printed 10 in-slice files, exit 0, under 1 second (0.97 s wall including startup); `--config A0` also works, exit 0.

`./bin/indexer ann-overlap --model qwen3-embedding:0.6b --kind chunk_header --queries probe --ef 200,400,800` and the same for `chunk_raw`: every width printed `chunk_mean=1.00 chunk_min=1.00 file_mean=1.00 file_min=1.00`, exit 0; the chosen `ef_search` is 200 (the smallest that clears the 95% chunk-level gate). The probe is a smoke gate and optimistic (queries sit near their own chunk); the binding gate is the S4 DEV run.

`EXPLAIN` of the retrieval query on the real data shows `Index Scan using emb_hnsw_qwen3_embedding_0_6b_chunk_header_1024`.

### S4 carry-list

DEV `--exact` parity (A0 and A1 Recall@10, nDCG@10, MRR identical under exact and HNSW, else raise `ef_search`, recorded in D-028); file-level overlap@10 reported; `short_results` per config; `search` and `ef_search` recorded in result JSON.
