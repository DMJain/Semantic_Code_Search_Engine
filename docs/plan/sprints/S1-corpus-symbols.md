# S1 - Corpus & symbols

## Goal
Pin the Kubernetes tag, walk and filter the corpus, extract symbols, benchmark summarization to fix the slice, then launch the overnight function-summary run (the critical path).

## Entry criteria
S0 done: Ollama running with `qwen2.5-coder:7b` pulled, CI green, `make doctor` passes.

## Links
[architecture.md#corpus-and-slice](../architecture.md#corpus-and-slice), [#stage-walk](../architecture.md#stage-walk), [#stage-parse](../architecture.md#stage-parse), [#stage-summarize](../architecture.md#stage-summarize), [#ollama-requests](../architecture.md#ollama-requests), [#token-estimate](../architecture.md#token-estimate), [#resumability](../architecture.md#resumability). Decisions: D-005, D-006, D-014, D-015.

## Tasks

### S1-T01 - Choose and pin the Kubernetes tag
- Why: eval labels depend on one fixed tag (D-006).
- Depends on: none
- Touches: `.env.example`, `docs/plan/decisions.md`
- Details: `git ls-remote --tags https://github.com/kubernetes/kubernetes 'v1.*'`; take the latest stable `v1.N.0` (no alpha/beta/rc) whose `go` directive in `go.mod` is <= `go version` locally. Set `KUBE_TAG` in `.env` and put the example value in `.env.example`. Run `make fetch-repo`. Confirm `vendor/` exists on disk and `staging/src/k8s.io/client-go` exists.
- Acceptance: `git -C data/kubernetes describe --tags` prints the tag; `grep '^go ' data/kubernetes/go.mod` is <= local Go; D-006 filled (tag, date, go directive, commit sha); `make fetch-repo` a second time is a no-op exit 0.
- Tests: none (fetch-repo tests exist).
- Out of scope: changing the tag later.
- Owner: agent

### S1-T02 - Migration 00003 and slice config
- Why: parse needs `raw_calls` and an idempotency marker; the slice must be configurable until frozen.
- Depends on: T01
- Touches: `internal/store/migrations/00003_symbols_raw_calls.sql`, `internal/walk/slice.go`
- Details: `ALTER TABLE symbols ADD COLUMN raw_calls text[] NOT NULL DEFAULT '{}'; ALTER TABLE files ADD COLUMN parsed_sha text; ALTER TABLE summaries ADD COLUMN reason text;` Down drops all three. `slice.go`: ordered list of path prefixes (the four from [corpus-and-slice](../architecture.md#corpus-and-slice)) and a drop list of subpath prefixes (empty now; used by T11).
- Acceptance: `make migrate` then `\d symbols` shows `raw_calls text[]`; `\d files` shows `parsed_sha`; `\d summaries` shows `reason`; `goose down` + `up` works.
- Tests: extend the migration integration test with an insert using both columns.
- Out of scope: edges columns (S2).
- Owner: agent

### S1-T03 - `indexer walk` with exclusions and slice marking
- Why: defines the corpus every later stage and the eval operate on.
- Depends on: T02
- Touches: `internal/walk/`, `cmd/indexer/walk.go`, `cmd/indexer/main.go`
- Details: implement [#stage-walk](../architecture.md#stage-walk). Exclusion rules as a table of named predicates (`vendor`, `zz_generated`, `pb_go`, `testdata`, `test_go`, `client_go_generated` = paths under `staging/src/k8s.io/client-go/{kubernetes,listers,informers,applyconfigurations}` plus any file containing `Code generated` + `DO NOT EDIT` in the first 10 lines). Count excluded files per rule. `sha` = sha256 of contents; `loc` = non-blank lines. Upsert in batches of 500 inside a transaction; delete rows whose path vanished. Flag `--dry-run` prints stats only.
- Acceptance: `go run ./cmd/indexer walk` prints per-rule excluded counts, total indexed files, slice files, LOC, and exits 0; running it twice reports `0 changed`; `psql -c "select count(*) from files where path like 'vendor/%' or path like '%_test.go' or path like '%zz_generated%' or path like '%.pb.go' or path like '%/testdata/%'"` returns 0; `select count(*) from files where in_slice` > 0 and every in-slice path starts with a slice prefix.
- Tests: unit test table for each exclusion predicate with positive/negative paths; golden test over a small fixture tree; integration test for idempotency (guarded by `TEST_DATABASE_URL`).
- Out of scope: parsing.
- Owner: agent

### S1-T04 - tree-sitter setup and function/method extraction
- Why: symbols are the base of chunks, edges and summaries.
- Depends on: T03
- Touches: `go.mod`, `internal/parse/`
- Details: add `github.com/smacker/go-tree-sitter` with the Go grammar (CGO; document `CGO_ENABLED=1` in the Makefile and confirm `go build ./...` and CI still pass). `parse.File(path, src) ([]Symbol, error)`: for `function_declaration` and `method_declaration` produce `Symbol{Kind: func|method, Name, QualifiedName, Receiver, Signature, StartLine, EndLine, Doc}`. Qualified name: `<import path>.<Name>` for functions, `<import path>.(*T).Name` / `.(T).Name` for methods (pointer vs value receiver preserved). Doc = contiguous `//` comment lines immediately above.
- Acceptance: `go test -count=1 ./internal/parse/...` passes; golden file test over `testdata/sample.go` (committed fixture) yields exactly the expected JSON; qualified name of `schedulePod` in the real tree is `k8s.io/kubernetes/pkg/scheduler.(*Scheduler).schedulePod`.
- Tests: golden JSON for pointer/value receivers, generics (`func F[T any]`), multiple return values, variadic, methods on embedded types, doc comments with blank-line gaps.
- Out of scope: structs/interfaces (T05), calls (T06), DB writes (T07).
- Owner: agent

### S1-T05 - struct and interface extraction
- Why: types are searchable symbols and feed file summaries.
- Depends on: T04
- Touches: `internal/parse/`
- Details: `type_declaration` with `struct_type` -> kind `struct`, `interface_type` -> kind `interface`; signature = `type Name struct{...}` header only (no field bodies; max 200 chars); doc as above; qualified name `<import path>.Name`. Grouped `type ( ... )` blocks yield one symbol per spec. Type aliases and other types are skipped.
- Acceptance: golden test passes; `go run ./cmd/indexer parse --stats` (after T07) lists non-zero struct and interface counts.
- Tests: golden for grouped type blocks, generic types, embedded interfaces.
- Out of scope: methods-on-interface extraction as separate symbols.
- Owner: agent

### S1-T06 - `raw_calls` extraction
- Why: summaries use raw call names (D-014).
- Depends on: T04
- Touches: `internal/parse/`
- Details: for each function/method body collect `call_expression` function-operand source text (e.g. `sched.cache.AssumePod`, `klog.V(2).InfoS` kept as written, trimmed to 80 chars), deduplicated, source order, capped at 40 per symbol. Calls inside nested function literals are included. Conversions and builtins (`len`, `make`, `append`, `string(x)`) are dropped by a fixed builtin list.
- Acceptance: golden test shows expected `raw_calls` for a fixture with method chains, builtins, closures; for `schedulePod` in the real tree the list is non-empty.
- Tests: golden.
- Out of scope: resolving anything (S2).
- Owner: agent

### S1-T07 - `indexer parse` command
- Why: stage wiring with idempotency and resume.
- Depends on: T05, T06
- Touches: `cmd/indexer/parse.go`, `cmd/indexer/main.go` (replace the stub, drop the "and build the call graph" text)
- Details: select `files` where `parsed_sha IS DISTINCT FROM sha`; parse each file; in one transaction per file delete that file's symbols and insert the new ones with `ON CONFLICT (qualified_name) DO UPDATE`, then set `parsed_sha = sha`. Duplicate qualified names within a file (e.g. `init`) get suffix `#<n>` by source order. Flags: `--limit N`, `--stats`. SIGINT finishes the current file and exits 0. Prints files parsed, symbols by kind, parse errors (files with tree-sitter ERROR nodes are still stored, count printed).
- Acceptance: `indexer parse` then `indexer parse` again prints `0 files to parse`; killing the first run mid-way (`kill -INT`) then rerunning completes with total symbols equal to an uninterrupted run (compare `select count(*) from symbols`); `select count(*) from symbols s join files f on f.id=s.file_id where f.in_slice` > 0.
- Tests: integration (guarded) idempotency + resume test using a 3-file fixture tree; unit test for `#n` suffix.
- Out of scope: edges.
- Owner: agent

### S1-T08 - Full walk + parse run and audit
- Why: verify symbol quality on the real corpus before building on it.
- Depends on: T07
- Touches: this file (Results)
- Details: run walk and parse on the pinned tag. Record counts: files, slice files, LOC, symbols by kind, in-slice functions, in-slice functions with body >= 3 lines. Sample 30 random in-slice symbols (seed 42) and verify name, receiver, line range, and doc against the source by eye; record mismatches.
- Acceptance: Results contains the counts and the 30-sample table with 0 unexplained mismatches (mismatches explained or fixed); parse error count recorded.
- Tests: none.
- Out of scope: fixes beyond parse bugs found.
- Owner: agent

### S1-T09 - Function summarizer core
- Why: the summarizer is the critical-path component (D-014).
- Depends on: T06, T07
- Touches: `internal/summarize/`, `cmd/indexer/summarize.go`
- Details: `prompt_version = "fn-v1"`. Prompt (system): "You are summarizing a Go function from Kubernetes for a code search index. Write 1-2 sentences describing what it does and why. No code, no preamble." User message: signature, doc, `Calls (raw): <raw_calls joined by ", ">`, then the code (cut to 6000 chars, marker `// ...truncated`). Temperature 0, `num_predict` 120, explicit `num_ctx` from config. `input_hash = sha256(prompt_version | code | signature | doc | raw_calls)`. Selection: in-slice function/method symbols with body >= 3 lines. States: `pending` (row created), `done`, `failed` with the reason in `summaries.reason` (column added in T02, `text` stays NULL on failure). Apply the [#ollama-requests](../architecture.md#ollama-requests) truncation check: `prompt_eval_count` more than 10% below the estimate or at `num_ctx` -> `failed`, reason `truncated`. Skip rows whose `done` row has the same `input_hash` and `prompt_version`. Concurrency flag `--concurrency 1|2`. Flags: `--level function`, `--limit N`, `--retry-failed`.
- Acceptance: `indexer summarize --level function --limit 20` creates 20 `done` rows (`select status,count(*) from summaries group by 1`); rerunning does 0 new calls; running with `--limit 5` and SIGINT mid-flight leaves no `pending` row older than the run (pending rows are re-picked next run); a forced-truncation unit test marks `failed`/`truncated`.
- Tests: unit tests for prompt building (golden), `input_hash` stability, selection filter; truncation path and success path against `httptest.Server`; guarded live test with `OLLAMA_URL`.
- Out of scope: file/package levels (S5), embedding summaries (S5).
- Owner: agent

### S1-T10 - Summarization throughput benchmark
- Why: the slice size is decided from measured throughput, not guesses (D-005).
- Depends on: T09
- Touches: `cmd/indexer/summarize.go` (`--bench`), this file (Results)
- Details: `indexer summarize --bench` samples 200 in-slice functions from the post-skip population (body >= 3 lines) with seed 7, runs the exact `fn-v1` production prompt with the chosen concurrency (run 1 then 2), and prints functions/hour, p50/p95 latency, output tokens. Then times 20 file-level prompts using a throwaway draft prompt (`file-bench`: input = that file's benchmark function summaries) and 3 package-level prompts the same way. Bench results are written to `eval/results/bench/summarize.json`, not to the `summaries` table. Also record the count of skipped (< 3 lines) functions in the slice.
- Acceptance: `eval/results/bench/summarize.json` exists with fields `fn_per_hour_c1`, `fn_per_hour_c2`, `file_per_hour`, `pkg_per_hour`, `skipped_fns`, `slice_fns_eligible`; the command does not insert rows into `summaries`.
- Tests: unit test for seeded sampling determinism (same seed -> same IDs).
- Out of scope: tuning the prompt for quality beyond one sanity read of 10 outputs.
- Owner: agent

### S1-T11 - [human] Decide and freeze the slice
- Why: the slice is the eval scope and must be final (D-005).
- Depends on: T10
- Touches: `internal/walk/slice.go`, `docs/plan/decisions.md`
- Details: project the combined run length: `T = eligible_fns/fn_per_hour + files/file_per_hour + packages/pkg_per_hour`. Apply the D-005 rule (at most 2 overnight runs, 20% safety margin). If the slice is too big, add subpath prefixes to the drop list in the D-005 order, re-run `walk`, recompute. Write the final size, the calculation, and the drop list in D-005, and the sha256 of the sorted `in_slice` path list. State "FINAL, never changes".
- Acceptance: D-005 filled; `select count(*) from symbols s join files f on f.id=s.file_id where f.in_slice and s.kind in ('func','method')` is between 15,000 and 25,000 (or the deviation is justified in D-005); path-list sha256 recorded.
- Tests: none.
- Out of scope: later slice changes (forbidden).
- Owner: human

### S1-T12 - Overnight-run tooling and launch
- Why: summaries are the critical path; the run must be pausable and sleep-proof.
- Depends on: T11
- Touches: `Makefile`, this file (Overnight run procedure)
- Details: `make summarize-run` = `caffeinate -i go run ./cmd/indexer summarize --level function --concurrency 1`. Pause = `Ctrl-C` or `make summarize-pause` (`pkill -INT -f "indexer summarize"`), which finishes the in-flight item and exits 0; resume = run `make summarize-run` again (rows `done` with unchanged `input_hash` are skipped). `make summarize-status` prints counts by status and an ETA from the last 100 completions. Launch the run and record start time, expected end, and count remaining.
- Acceptance: `make summarize-run` starts, `make summarize-status` shows `done` growing; `make summarize-pause` exits the run within 60s; `make summarize-run` resumes without redoing done rows (done count never decreases); `pmset -g assertions | grep -i caffeinate` shows the assertion while running.
- Tests: none beyond T09.
- Out of scope: file/package levels.
- Owner: agent

## Overnight run procedure
This is the single source for the run; later sprints link here.
1. Start: `make summarize-run`. Use it overnight with the lid open on power. Check: `make summarize-status`.
2. Pause before any heavy Ollama job (embedding runs, S3-T09; `ollama` model swaps thrash memory): `make summarize-pause`, wait for exit.
3. Resume: `make summarize-run` (idempotent).
4. Done when `make summarize-status` shows 0 pending and failed < 2% of eligible; run `summarize --level function --retry-failed` once for failures.

## Measurements to record
Written to this file's Results and `eval/results/bench/summarize.json`:
- Walk stats (files, slice files, LOC, excluded per rule), symbol counts by kind, parse errors.
- Benchmark: functions/hour (c1, c2), file/package prompts per hour, skipped-function count.
- Final slice size and projected combined summarization run length (D-005); run start timestamp.
- Indexing metrics for `walk` and `parse` wall time ([eval-protocol.md#indexing-metrics](../eval-protocol.md#indexing-metrics)).

## Exit criteria / Definition of Done
Tag pinned (D-006); walk and parse idempotent; slice FINAL (D-005); function-summary run launched and verified pausable; ROADMAP updated.

## Risks + mitigations
- CGO/tree-sitter build issues on CI: add `CGO_ENABLED=1` and verify in CI in T04; fallback is `gotreesitter` bindings, not Python.
- Throughput below 1k fn/h: the D-005 shrink order applies; do not start the run before T11.
- Prompt truncation on huge functions: 6000-char cut plus the truncation check.
- Mac sleeps: `caffeinate -i`; keep on power.

## Demo check
`indexer walk && indexer parse --stats` prints stats; `psql` shows the qualified name `k8s.io/kubernetes/pkg/scheduler.(*Scheduler).schedulePod`; `make summarize-status` shows the run progressing.

## Results
(empty)
