# S8 - Experiments & ship

## Goal
Run the embedder (E1 vs E2) and MRL (M1) experiments on DEV under A3n, then execute the single final TEST run of every configuration, grade Q&A blind, generate `eval/report.md`, write the README, and record the demo video and GIF.

## Entry criteria
S7 done. TEST hash recorded (D-010) and `eval freeze --verify` passes. Tuned values from S5/S6 recorded in D-020. All earlier DEV results exist (A0-A4, A3n, B0).

## Links
[eval-protocol.md](../eval-protocol.md): [#configurations](../eval-protocol.md#configurations), [#ann-overlap](../eval-protocol.md#ann-overlap), [#freeze](../eval-protocol.md#freeze), [#reproducibility](../eval-protocol.md#reproducibility), [#qa-evaluation](../eval-protocol.md#qa-evaluation), [#indexing-metrics](../eval-protocol.md#indexing-metrics), [#limitations-to-be-repeated-in-readme](../eval-protocol.md#limitations-to-be-repeated-in-readme). [architecture.md#embedding-variants](../architecture.md#embedding-variants), [#vector-indexes](../architecture.md#vector-indexes). Decisions: D-003, D-007, D-020, D-028, D-029, D-031, D-037, D-041. Run procedure: [S1 overnight procedure](S1-corpus-symbols.md#overnight-run-procedure) (embedding jobs).

## Phases
Phase 1 (experiments on DEV): T01-T04. Phase 2 (final): T05-T09 in order, no code changes between T05 and T09. Phase 3 (ship): T10-T18.

## Tasks

### S8-T01 - Groq model check and DEV re-baseline
- Why: all reported numbers must come from one model ([#reproducibility](../eval-protocol.md#reproducibility), D-007).
- Depends on: none
- Touches: `docs/plan/decisions.md` (D-007), `eval/results/*/dev.json`
- Details: check the Groq model recorded in D-007 is still available (`GET /openai/v1/models`). If retired or changed: record the new ID and date in D-007, clear nothing from `llm_cache` (keys include the model), and re-run DEV for every config that makes LLM calls (A3, A4 and the DEV Q&A dry run) with the new model. Otherwise record "unchanged" with the date.
- Acceptance: D-007 has a dated entry; `jq -r .groq_model eval/results/{A3,A4}/dev.json` prints the same ID for both and it equals the ID in D-007.
- Tests: none.
- Out of scope: new prompt versions.
- Owner: agent

### S8-T02 - Embed the alternate model (operational)
- Why: E2 needs `nomic-embed-text` vectors ([#embedding-variants](../architecture.md#embedding-variants), D-029).
- Depends on: none
- Touches: this file (Results)
- Details: for `nomic-embed-text`: `doctor --probe-all`; `indexer embed --model nomic-embed-text --kinds chunk_header`; `indexer vindex --model nomic-embed-text --kind chunk_header`. Summary kinds are not embedded for nomic because A3n uses no summaries. Pause the summary run only if it is still active (it should be finished). Record items/s and wall time per model.
- Acceptance: `select target_kind,count(*) from embeddings where model='nomic-embed-text' group by 1` shows `chunk_header` equal to the slice chunk count minus the `embed_failures` rows of that kind; `EXPLAIN` uses the model's HNSW index.
- Tests: none.
- Out of scope: vectors outside the slice (D-029); summary vectors for nomic.
- Owner: agent

### S8-T03 - MRL truncated indexes and M1 config
- Why: M1 compares native dim vs 256d without re-embedding ([#vector-indexes](../architecture.md#vector-indexes)).
- Depends on: T02
- Touches: `cmd/indexer/vindex.go`, `internal/retrieve/flat.go`, `internal/vec/truncate.go`, `internal/evalconfig/`
- Details: confirm MRL support per model from D-003 (qwen3 and nomic must both be confirmed on their cards; a model without MRL support gets no M1 config). For MRL models: `indexer vindex --model M --kind K --dim 256` creates `USING hnsw ((l2_normalize(subvector(vec, 1, 256))::vector(256)) vector_cosine_ops) WHERE model='M' AND target_kind='K'`. Retrievers gain `Dim` in config: when `Dim < native`, query vectors are truncated and re-normalized in Go (`vec.Truncate(v, n)` in the new `internal/vec` package, which re-normalizes; Phase 2 reuses it) and the SQL uses the same expression so the index matches. Register `M1-qwen3-256` and `M1-nomic-256`: A3n with that model at 256 dimensions (their native-dim counterparts are E1 and E2).
- Acceptance: unit test: `Truncate` returns unit-norm vectors of length 256 and equals the first 256 components scaled; `EXPLAIN` of a 256d query uses `emb_hnsw_*_256`; `indexer search --config M1-qwen3-256 "<q>"` returns 10 files.
- Tests: unit tests for truncate/normalize; guarded integration test for the expression-index plan.
- Out of scope: models without MRL.
- Owner: agent

### S8-T04 - E and M configs on DEV; ANN overlap per model; ef_search
- Why: ANN gate ([#ann-overlap](../eval-protocol.md#ann-overlap)) and the DEV evidence that Phase 2 uses for its model and dimension choice (D-041).
- Depends on: T03, T01
- Touches: `internal/evalconfig/`, `eval/results/{E2,M1-qwen3-256,M1-nomic-256}/dev.json`, `docs/plan/decisions.md` (D-028)
- Details: E1 is A3n with `qwen3-embedding:0.6b` (an alias of the existing `A3n` DEV result, no new run); E2 is A3n with `nomic-embed-text`; M1 variants as in T03 (D-031). Run `ann-overlap --queries eval/queries.dev.jsonl` per model and variant; tune `ef_search` per model if < 95% (log the sweep). Run all on DEV, `eval compare` E2 vs E1 and each M1 vs its native-dim config (paired bootstrap CIs, DEV only), `eval report`. Record all final tuned values in D-028: weights and thresholds (from D-020), `ef_search` per model, dims. The E1/E2/M1 DEV comparison tables are the inputs to D-041; do not apply the rule here.
- Acceptance: each E2/M1 config has `dev.json` and `eval compare` output with CIs; overlap >= 95% for every `(model, dim)` used or the shortfall is recorded; D-028 filled with all tuned values.
- Tests: none.
- Out of scope: any parameter change after D-028 is written.
- Owner: agent

### S8-T05 - Pre-final checklist and code freeze
- Why: the final run must be unambiguous.
- Depends on: T04
- Touches: this file (Results)
- Details: `git status` clean and a tag `final-eval` on the commit; `eval freeze --verify`; D-028 filled; `make test`, `make lint`, `go vet` green; DB has all vectors; Groq reachable; Ollama up; `llm_cache` not pruned. From here until T09 completes, only result files change.
- Acceptance: `git describe --tags` prints `final-eval`; `go run ./cmd/eval freeze --verify` exits 0; all commands green (`make test`, `make lint`, `go vet`, `make verify-slice`); `! grep -A3 -E "^## D-0(20|28) " docs/plan/decisions.md | grep -q PENDING` exits 0 (tuned values recorded); Results lists the commit.
- Tests: none.
- Out of scope: new features.
- Owner: agent

### S8-T06 - Final TEST run, every configuration, once
- Why: the headline numbers ([#freeze](../eval-protocol.md#freeze)).
- Depends on: T05
- Touches: `eval/results/<config>/test.json`
- Details: for each of A0, A1, B0, A2, A3, A3n, A4, E2, M1-qwen3-256, M1-nomic-256 (E1 is A3n, run once as A3n): `go run ./cmd/eval run --config <C> --split test --final`. If any run fails for infrastructure reasons, rerun only with `--rerun-reason infra:<text>` (recorded) and never after looking at metrics for tuning. Then `eval compare` of every config vs A0 (plus B0 vs A0, A3 vs A2, A3n vs A1, E2 vs E1, each M1 vs its native-dim config). Commit results (the `llm_cache` export is written in T09, after the Q&A runs).
- Acceptance: every config has `test.json` with 35 per-query rows and the same `query_file_sha256`; `jq .groq_model` identical across LLM configs; `ls eval/results/*/test.json | wc -l` equals the number of surviving configurations: 10 when nothing was cut by the ROADMAP priority list (A0, A1, B0, A2, A3, A3n, A4, E2, M1-qwen3-256, M1-nomic-256), otherwise 10 minus the cut ones (the list is recorded in Results).
- Tests: none.
- Out of scope: reruns to improve numbers (forbidden).
- Owner: agent

### S8-T07 - Q&A TEST run and blind sheet
- Why: [#qa-evaluation](../eval-protocol.md#qa-evaluation).
- Depends on: T06
- Touches: `eval/results/qa/test/answers.json`, `eval/qa/test/sheet.md`
- Details: `eval qa-run --split test --final` (the 15 queries chosen in S7-T10, both systems; fails closed and resumes across days if S7-T11 projected more than one quota window; no Ollama fallback), `eval qa-sheet --split test --seed 77`; confirm the leak-grep test passes on the generated sheet; keep `key.json` out of git until grading ends.
- Acceptance: `answers.json` has 2 x N answers (N = 15, or 10 if the ROADMAP priority list cut Q&A grading); `grep -ciE "multihop|single|hops|trace|tokens" eval/qa/test/sheet.md` = 0; `key.json` is not tracked (`git status --ignored` shows it ignored).
- Tests: none.
- Out of scope: grading.
- Owner: agent

### S8-T08 - [human] Grade 30 answers blind
- Why: answer quality metric.
- Depends on: T07
- Touches: `eval/qa/test/grades.csv`
- Details: grade each answer 0/1/2 against `eval/qa/rubric.md` without opening traces or the key.
- Acceptance: CSV has 2 x N rows (as in T07) with grades in {0,1,2}.
- Tests: none.
- Out of scope: reading the key before finishing.
- Owner: human

### S8-T09 - Q&A summary
- Why: grades joined to systems, cost reported alongside.
- Depends on: T08
- Touches: `eval/results/qa/test/summary.json`
- Details: `eval qa-grade --split test --in eval/qa/test/grades.csv`; the summary includes mean grade, grade distribution, citation precision, hops (multihop), tokens and LLM calls per answer, grade-vs-cost table, paired bootstrap CI for the grade difference (multihop minus single). Commit `key.json` only after this step. Then run `eval cache-export --out eval/results/llm_cache.jsonl.gz` (covers the TEST run and the Q&A runs) and commit it.
- Acceptance: `summary.json` has both systems with the fields above; the CI is present and flagged if it includes 0; the export's line count equals `select count(*) from llm_cache`.
- Tests: none.
- Out of scope: LLM judge (T10).
- Owner: agent

### S8-T10 - Optional LLM-as-judge with agreement
- Why: secondary check ([#qa-evaluation](../eval-protocol.md#qa-evaluation)). Cut-list item.
- Depends on: T09
- Touches: `cmd/eval/judge.go`, `eval/results/qa/test/judge.json`
- Details: `prompt_version = "judge-v1"`, same rubric text, temperature 0, one call per answer on the Groq model recorded in D-007; report exact-match agreement and Cohen's kappa versus the human grades. Never replaces human grades. Re-run `eval cache-export` after the judge calls.
- Acceptance: `judge.json` contains 30 judge grades, exact agreement %, kappa; report text states it is secondary.
- Tests: unit test for kappa on known tables.
- Out of scope: using judge grades in headline numbers.
- Owner: agent

### S8-T11 - Indexing metrics report
- Why: [#indexing-metrics](../eval-protocol.md#indexing-metrics) in one place.
- Depends on: T06
- Touches: `cmd/eval/indexing.go`, `eval/results/indexing/result.json`
- Details: `eval indexing-report` combines two sources. From the database: vector counts per `(model, kind)`, `pg_database_size`, edge and chunk counts. From `eval/results/indexing/stages.json`, which you fill once by hand in this task from each sprint's Results section: wall seconds per stage and summaries per hour per level.
- Acceptance: `result.json` has `vectors[model][kind]`, `db_size_bytes`, `stages[stage].wall_seconds`, `summaries_per_hour[level]`; every stage in {walk, parse, graph, callgraph-gt, chunk, summarize-function, summarize-file, summarize-package, embed} has a wall time.
- Tests: unit test for the report builder with fixtures.
- Out of scope: new measurements.
- Owner: agent

### S8-T12 - Generate and review `eval/report.md`
- Why: the honest benchmark deliverable.
- Depends on: T09, T11, T06
- Touches: `eval/report.md`
- Details: `eval report`; review that every config row has CIs, that "no demonstrated difference" appears where the CI includes 0, that per-route tables mention wide CIs, and that A0 is shown first even where it wins. Add the call-graph table (S2), router and system metrics (S6), Q&A (T09), indexing (T11). Do not edit numbers by hand; changes go through the generator.
- Acceptance: `eval report` twice gives byte-identical output; the report has sections for retrieval ladder, E, M1, router, system, call graph, Q&A, indexing, limitations; no section produced by S0-S8 prints `not yet measured` (sections for S9 and Phase 2 are omitted until their JSON exists).
- Tests: golden report test extended with the full section list.
- Out of scope: README.
- Owner: agent

### S8-T13 - README: setup, architecture diagram
- Why: deliverable: reproducible local setup and the Mermaid architecture diagram.
- Depends on: none
- Touches: `README.md`
- Details: sections: overview, prerequisites (Docker, Go version, Ollama + models), setup steps (`make up`, `make migrate`, `make doctor`, `.env`, `make fetch-repo`, each indexer stage in order with expected wall times from Results), run the server, run the eval. Mermaid diagram: indexer pipeline (stages -> Postgres), query path (router -> retrievers -> RRF -> Q&A loop -> trace), eval harness. Link to `eval/report.md`.
- Acceptance: following the README top to bottom on the current machine reaches a running `/healthz` (timed, recorded in Results); the Mermaid block renders (check with GitHub preview or `mmdc` once); every command in the README exists (`grep` of make targets/subcommands in a script `scripts/check-readme-commands.sh` passes).
- Tests: the command-existence script.
- Out of scope: Design Decisions text.
- Owner: agent

### S8-T14 - README: design decisions, evaluation, what I'd do differently, limitations
- Why: deliverable sections; honest reporting.
- Depends on: T12, T13
- Touches: `README.md`
- Details: Design Decisions (each from `decisions.md` with the reason: slice, VTA ground truth, raw-call summaries, RRF without reranker, frozen TEST); Evaluation (headline table copied from `eval/report.md` by link plus 3 sentences, including where the system does NOT beat the baseline); What I'd Do Differently (LLM reranker, whole-repo summaries, better type resolution/gopls-based graph, larger eval set, multiple labelers); Limitations (from [eval-protocol.md limitations](../eval-protocol.md#limitations-to-be-repeated-in-readme)). Numbers must match `eval/report.md` (script check).
- Acceptance: the four sections are present with those exact titles (T13 adds setup and the diagram); `scripts/check-readme-numbers.sh` (extracts the headline table's numbers and compares them to `eval/results/*/test.json`) exits 0.
- Tests: the numbers script.
- Out of scope: new analyses.
- Owner: agent

### S8-T15 - Demo script and recording checklist
- Why: video deliverable.
- Depends on: T14
- Touches: `docs/demo.md`
- Details: 2-3 minute script with timings: (0:00) problem and corpus, (0:20) lexical query, (0:40) structural query with ambiguous flag, (1:00) semantic query, (1:30) `/ask` with hops and citations, (2:10) trace page, (2:30) eval numbers incl. where baseline wins. Checklist: DB warmed, Groq cache primed with the exact demo queries, Ollama models loaded, terminal font size, window layout, notifications off, a fallback take with Groq disabled.
- Acceptance: `docs/demo.md` has timed script rows summing to <= 3:00 and a checklist of >= 8 items; every demo query is run once to confirm it works (listed with trace IDs in Results).
- Tests: none.
- Out of scope: recording.
- Owner: agent

### S8-T16 - [human] Record the demo video
- Why: deliverable (no live hosting).
- Depends on: T15
- Touches: `docs/demo/` (video link/file)
- Details: record per script; at most 3 takes; export mp4 <= 100MB or upload privately and record the link.
- Acceptance: video file or link recorded in README; duration between 2:00 and 3:00.
- Tests: none.
- Out of scope: editing effects.
- Owner: human

### S8-T17 - [human] GIF
- Why: deliverable.
- Depends on: T16
- Touches: `docs/demo/demo.gif`, `README.md`
- Details: 10-15 s loop of the `/ask` trace page from the video; <= 8MB; embedded at the top of README.
- Acceptance: file exists and is under 8MB (`ls -l`); README renders it.
- Tests: none.
- Out of scope: further polish.
- Owner: human

### S8-T18 - Fresh-clone reproducibility check
- Why: "reproducible locally".
- Depends on: T14
- Touches: this file (Results)
- Details: clone the repo to a scratch directory, follow the README with `make reset-db`, run the cheap parts (migrate, doctor, walk and parse with `--limit`, `eval validate --strict`, `eval freeze --verify`, `eval report`), confirm nothing depends on untracked files. Fix README gaps.
- Acceptance: every step exits 0 from a fresh clone; the list of fixes (if any) is in Results.
- Tests: none.
- Out of scope: reindexing the full corpus.
- Owner: agent

## Measurements to record
- Final TEST metrics with CIs for every config: `eval/results/<config>/test.json`, `eval/report.md`.
- ANN overlap@10 per model/dim (DEV) and `ef_search`, E/M DEV comparison, D-028 tuned values.
- Q&A summary (grades, citation precision, hops, tokens, calls): `eval/results/qa/test/summary.json`; judge agreement if T10 ran.
- Indexing summary: `eval/results/indexing/result.json`.
- README setup steps as verified on this machine.

## Exit criteria / Definition of Done
All TEST results committed once per config; report generated and reviewed; README has setup steps, the diagram and the four required sections; video and GIF recorded; fresh-clone check passes; ROADMAP marks all sprints done.

## Risks + mitigations
- Groq unavailable during the final run: no fallback in TEST runs; wait for the quota and resume (an infrastructure rerun needs `--rerun-reason infra:<text>`, never to improve numbers).
- If scope must shrink, follow the Phase 1 priority list in ROADMAP.md (M1 first). Dropping M1 or E2 removes the evidence Phase 2 uses for its model and dimension choice; D-041 then falls back to qwen3 at its native dimension.
- Results unflattering: reported as is (this is a deliverable).

## Demo check
`make up && make doctor && go run ./cmd/server`, open the UI, run the demo queries; `cat eval/report.md`; watch the video.

## Results
(empty)
