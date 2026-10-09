# S1 - Corpus & symbols

## Goal
Pin the Kubernetes tag, walk and filter the corpus, extract symbols, benchmark summarization and probe the mining yield to fix the slice, then launch the overnight function-summary run (the critical path).

## Entry criteria
S0 done: Ollama running with `qwen2.5-coder:7b` pulled, CI green, `make doctor` passes. A fine-grained read-only `GITHUB_TOKEN` is in `.env` (needed from S1-T12).

## Links
[architecture.md#corpus-and-slice](../architecture.md#corpus-and-slice), [#stage-walk](../architecture.md#stage-walk), [#stage-parse](../architecture.md#stage-parse), [#stage-summarize](../architecture.md#stage-summarize), [#ollama-requests](../architecture.md#ollama-requests), [#token-estimate](../architecture.md#token-estimate), [#resumability](../architecture.md#resumability). Decisions: D-005, D-006, D-014, D-015, D-058.

## Tasks

### S1-T01 - Choose and pin the Kubernetes tag
- Why: eval labels depend on one fixed tag (D-006).
- Depends on: none
- Touches: `.env.example`, `docs/plan/decisions.md`
- Details: `git ls-remote --tags https://github.com/kubernetes/kubernetes 'v1.*'`; take the latest stable `v1.N.0` (no alpha/beta/rc) whose `go` directive in `go.mod` is <= `go version` locally. Set `KUBE_TAG` in `.env` and put the example value in `.env.example`. Run `make fetch-repo`. Confirm `vendor/` exists on disk and `staging/src/k8s.io/client-go` exists.
- Acceptance: `git -C data/kubernetes describe --tags` prints the tag; `test -d data/kubernetes/vendor && test -d data/kubernetes/staging/src/k8s.io/client-go` succeeds; `printf '%s\n%s\n' "$(grep '^go ' data/kubernetes/go.mod | cut -d' ' -f2)" "$(go env GOVERSION | sed 's/^go//')" | sort -V -C` succeeds (the directive is <= local Go); D-006 filled (tag, date, go directive, commit sha); `make fetch-repo` a second time is a no-op exit 0.
- Tests: none (fetch-repo tests exist).
- Out of scope: changing the tag later.
- Owner: agent

### S1-T02 - Migration 00003 and slice config
- Why: parse needs `raw_calls` and an idempotency marker; the slice must be configurable until frozen.
- Depends on: T01
- Touches: `internal/store/migrations/00003_symbols_raw_calls.sql`, `internal/walk/slice.go`
- Details: `ALTER TABLE symbols ADD COLUMN raw_calls text[] NOT NULL DEFAULT '{}'; ALTER TABLE files ADD COLUMN parsed_sha text; ALTER TABLE summaries ADD COLUMN reason text; CREATE UNIQUE INDEX summaries_uniq ON summaries (level, target_id, prompt_version);` Down drops all four. `slice.go`: ordered list of path prefixes (the four from [corpus-and-slice](../architecture.md#corpus-and-slice)) and a drop list of subpath prefixes (empty now; used by T13).
- Acceptance: `make migrate` then `\d symbols` shows `raw_calls text[]`; `\d files` shows `parsed_sha`; `\d summaries` shows `reason`; `make migrate-down migrate-up` exits 0 and `\d summaries` is unchanged; inserting a second `summaries` row with the same `(level, target_id, prompt_version)` fails.
- Tests: extend the migration integration test with an insert using both columns and a duplicate-summary insert that must be rejected.
- Out of scope: edges columns (S2).
- Owner: agent

### S1-T03 - `indexer walk` with exclusions and slice marking
- Why: defines the corpus every later stage and the eval operate on.
- Depends on: T02
- Touches: `internal/walk/`, `cmd/indexer/walk.go`, `cmd/indexer/main.go`
- Details: implement [#stage-walk](../architecture.md#stage-walk). Exclusion rules as a table of named predicates (`vendor`, `zz_generated`, `pb_go`, `testdata`, `test_go`, `client_go_generated` = paths under `staging/src/k8s.io/client-go/{kubernetes,listers,informers,applyconfigurations}` plus any file containing `Code generated` + `DO NOT EDIT` in the first 10 lines) and `build_constraint` = `go/build.Context{GOOS:"linux", GOARCH:"amd64", CgoEnabled:false}.MatchFile` rejects the file (the same context as `callgraph-gt`, D-058). Count excluded files per rule. `sha` = sha256 of contents; `loc` = non-blank lines. Upsert in batches of 500 inside a transaction; delete rows whose path vanished. Flag `--dry-run` prints stats only.
- Acceptance: `go run ./cmd/indexer walk` prints per-rule excluded counts, total indexed files, slice files, LOC, and exits 0; running it twice reports `0 changed`; `psql -c "select count(*) from files where path like 'vendor/%' or path like '%_test.go' or path like '%zz_generated%' or path like '%.pb.go' or path like '%/testdata/%'"` returns 0; `select count(*) from files where in_slice` > 0 and every in-slice path starts with a slice prefix; `select count(*) from files where path ~ '_(windows|darwin|freebsd|plan9)\.go$'` returns 0.
- Tests: unit test table for each exclusion predicate (including a `_windows.go` file and a `//go:build windows` file as negatives) with positive/negative paths; golden test over a small fixture tree; integration test for idempotency (guarded by `TEST_DATABASE_URL`).
- Out of scope: parsing.
- Owner: agent

### S1-T04 - tree-sitter setup and function/method extraction
- Why: symbols are the base of chunks, edges and summaries.
- Depends on: T03
- Touches: `go.mod`, `Makefile`, `.github/workflows/ci.yml`, `internal/parse/`
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
- Details: select `files` where `parsed_sha IS DISTINCT FROM sha`; parse each file; packages with any stale file are re-parsed completely: compute qualified names for the whole package first, where duplicates (several `init`, same-named functions in different files of one package) get the suffix `#<n>` from the second occurrence ordered by `(path, start_line)`; then, in one transaction per file, delete that file's symbols and insert the new ones with `ON CONFLICT (qualified_name) DO UPDATE`, then set `parsed_sha = sha`. Flags: `--limit N`, `--stats`. SIGINT finishes the current file and exits 0. Prints files parsed, symbols by kind, parse errors (files with tree-sitter ERROR nodes are still stored, count printed).
- Acceptance: `indexer parse` then `indexer parse` again prints `0 files to parse`; killing the first run mid-way (`kill -INT`) then rerunning completes with total symbols equal to an uninterrupted run (compare `select count(*) from symbols`); `select count(*) from symbols s join files f on f.id=s.file_id where f.in_slice` > 0; `select qualified_name,count(*) from symbols group by 1 having count(*)>1` returns no rows.
- Tests: integration (guarded) idempotency + resume test using a 3-file fixture tree; unit test for the cross-file `#n` suffix numbering (two files, same package, two `init` functions each).
- Out of scope: edges.
- Owner: agent

### S1-T08 - Full walk + parse run and audit
- Why: verify symbol quality on the real corpus before building on it.
- Depends on: T07
- Touches: this file (Results)
- Details: run walk and parse on the pinned tag. Record counts: files, slice files, LOC, symbols by kind, in-slice functions, in-slice functions with body >= 3 lines. Sample 30 random in-slice symbols (seed 42) and verify them with a script that re-reads the source at `start_line..end_line` and checks that the first line contains the symbol name and the receiver type and that the doc comment text equals the stored doc; record and explain mismatches.
- Acceptance: Results contains the counts and the 30-sample table with 0 unexplained mismatches (mismatches explained or fixed); parse error count recorded.
- Tests: none.
- Out of scope: fixes beyond parse bugs found.
- Owner: agent

### S1-T09 - Function summary prompt, Ollama call and request guards
- Why: the summarizer is the critical-path component (D-014); this task is its pure part (no database).
- Depends on: T06, T07
- Touches: `internal/summarize/prompt.go`, `internal/summarize/ollama.go`, `internal/summarize/guard.go`
- Details: `prompt_version = "fn-v1"`. System prompt: "You are summarizing a Go function from Kubernetes for a code search index. Write 1-2 sentences describing what it does and why. No code, no preamble." User message: signature, doc, `Calls (raw): <raw_calls joined by ", ">`, then the code read from `KUBE_DIR` by the symbol's file and line range (cut to 6000 chars, marker `// ...truncated`). Ollama `/api/chat`, temperature 0, `num_predict` 120, explicit `num_ctx` from config (`OLLAMA_SUMMARY_NUM_CTX`, default 4096). `input_hash = sha256(prompt_version | code | signature | doc | raw_calls)`. Selection: in-slice function/method symbols with `end_line - start_line >= 3` (D-014). Guards per [#ollama-requests](../architecture.md#ollama-requests): pre-send, estimated tokens > 0.8 x `num_ctx` -> not sent, reason `too_large`; post-send, `prompt_eval_count >= num_ctx - num_predict` -> reason `truncated`. Other failure reasons: `http_error`, `empty_output`.
- Acceptance: `go test -count=1 ./internal/summarize/...` passes with: golden prompt; stable `input_hash`; selection test (a symbol with `end_line - start_line = 2` is excluded, `= 3` is included); a request estimated at 0.81 x `num_ctx` is not sent (the `httptest.Server` records 0 requests) and returns `too_large`; a response with `prompt_eval_count = num_ctx - num_predict` returns `truncated`; a normal response returns the text.
- Tests: the cases above plus a guarded live test with `OLLAMA_URL`.
- Out of scope: database state and CLI (T10); file/package levels (S5).
- Owner: agent

### S1-T10 - `indexer summarize` command: state, resume, flags
- Why: idempotent, resumable stage wiring for the critical-path run (D-014).
- Depends on: T09, T02
- Touches: `cmd/indexer/summarize.go`, `internal/summarize/run.go`
- Details: for each selected symbol upsert the `summaries` row on `summaries_uniq (level, target_id, prompt_version)` with `model` = the summary model, states `pending` (row created), `done`, `failed` (reason in `summaries.reason`, `text` stays NULL). Skip rows that are `done` with the same `input_hash`; re-pick `pending` and (with `--retry-failed`) `failed` rows. Order by symbol id. Flags: `--level function`, `--limit N`, `--concurrency 1|2`, `--retry-failed`. SIGINT/SIGTERM finish the in-flight item, commit and exit 0.
- Acceptance: `indexer summarize --level function --limit 20` creates 20 `done` rows (`select status,count(*) from summaries group by 1`); rerunning makes 0 new Ollama calls; `select level,target_id,prompt_version,count(*) from summaries group by 1,2,3 having count(*)>1` returns no rows; `--limit 5` with SIGINT mid-flight leaves no `pending` row older than the run; a symbol whose estimate exceeds 0.8 x `num_ctx` ends as `failed` with reason `too_large` and no HTTP call.
- Tests: guarded integration tests (idempotency, resume, `too_large` row) against `httptest.Server`; concurrency 2 test with `-race`.
- Out of scope: file/package levels and embedding summaries (S5).
- Owner: agent

### S1-T11 - Summarization throughput benchmark
- Why: the slice size is decided from measured throughput, not guesses (D-005).
- Depends on: T10
- Touches: `cmd/indexer/summarize.go` (`--bench`), this file (Results)
- Details: `indexer summarize --bench` samples 200 in-slice functions from the post-skip population (`end_line - start_line >= 3`) with seed 7, runs the exact `fn-v1` production prompt with the chosen concurrency (run 1 then 2), and prints functions per hour, p50/p95 latency, output tokens. Then times 20 file-level prompts and 3 package-level prompts with throwaway draft prompts (`file-bench`, `pkg-bench`): the 20 files are stratified by eligible-function count (seed 7); first summarize ALL eligible functions of those files with `fn-v1` (not stored) so the file prompt inputs have realistic length. Results go to `eval/results/bench/summarize.json`, not to the `summaries` table. Also record the count of skipped functions in the slice.
- Acceptance: `eval/results/bench/summarize.json` exists with fields `fn_per_hour_c1`, `fn_per_hour_c2`, `file_per_hour`, `pkg_per_hour`, `skipped_fns`, `slice_fns_eligible`; the command does not insert rows into `summaries`; the 20 files cover at least 3 distinct eligible-function-count strata (fields `file_strata`).
- Tests: unit test for seeded sampling determinism (same seed -> same IDs) and stratification.
- Out of scope: tuning the prompt for quality beyond one sanity read of 10 outputs.
- Owner: agent

### S1-T12 - Mining-yield probe on the candidate slice
- Why: the slice is frozen forever at T13 and the 25 semantic queries must be minable inside it (D-005).
- Depends on: T03, T11
- Touches: `cmd/eval/mineyield.go`, `internal/config/config.go`, `.env.example`, `.gitignore`, `docs/plan/decisions.md` (D-005)
- Details: add `GITHUB_TOKEN` to config and `.env.example` (empty, commented, never committed). `eval mine-yield [--window-months 12]` queries GitHub REST search in weekly `merged:` windows with `base:master` and `linked:issue` (search returns at most 1,000 results per query: a window that hits the cap is split in halves automatically; secondary limit: throttle to 20 requests per minute and sleep until `X-RateLimit-Reset` when remaining is 0); raw responses are cached under `eval/candidates/cache/` (add to `.gitignore`). For each PR fetch the changed files and count PRs whose changed non-test non-generated files (walk predicates including `build_constraint`) number 1-5, all inside the CANDIDATE slice (current `slice.go`), all existing at the pinned tag. No leakage filter (S4-T06). Report tier 1 (12 months) and tier 2 (`--window-months 18`) yields and the contribution per slice prefix; write `eval/results/bench/mine-yield.json`. S4-T05 reuses this HTTP client. Without a token: exit non-zero with `set GITHUB_TOKEN (read-only, public repos) in .env`.
- Acceptance: unset token -> exact message above and non-zero exit; with the token, `go run ./cmd/eval mine-yield` writes JSON with `tier1`, `tier2`, `by_prefix` and `windows_at_cap` = 0 (capped windows are split until none remains); a second run makes 0 HTTP calls; the numbers are copied into D-005.
- Tests: `httptest.Server` for pagination, cap splitting, rate-limit sleep.
- Out of scope: leakage filtering and query choice (S4); changing the slice (T13).
- Owner: agent

### S1-T13 - [human] Decide and freeze the slice
- Why: the slice is the eval scope and must be final (D-005).
- Depends on: T11, T12
- Touches: `internal/walk/slice.go`, `docs/plan/decisions.md`
- Details: (1) Size: project the combined run length `T = eligible_fns/fn_per_hour + files/file_per_hour + packages/pkg_per_hour`. Apply the D-005 rule (at most 2 overnight runs, 20% safety margin); if too big, add subpath prefixes to the drop list in the D-005 order, re-run `walk`, recompute. (2) Yield: read `mine-yield.json`; tier 1 plus tier 2 must be at least 30. If short, never grow the slice beyond the compute budget; in order: (a) change the composition within the same budget (for example keep client-go and drop more pkg/controller subpackages), then re-run `walk`, `mine-yield` and the projection; (b) extend the mining window further back (`--window-months 24`, then 30). The KEP cap (5 of 25) stays. (3) Write the final size, the calculation, the yield outcome and the drop list in D-005, plus the sha256 of the sorted `in_slice` path list. State "FINAL, never changes".
- Acceptance: D-005 filled; `select count(*) from symbols s join files f on f.id=s.file_id where f.in_slice and s.kind in ('func','method')` is between 15,000 and 25,000 (or the deviation is justified in D-005); D-005 holds `tier1 + tier2 >= 30` (or the window extension used); path-list sha256 recorded.
- Tests: none.
- Out of scope: later slice changes (forbidden).
- Owner: human

### S1-T14 - Overnight-run tooling, quality gate and launch
- Why: summaries are the critical path; the run must be pausable, sleep-proof and checked for quality before it consumes the compute budget.
- Depends on: T13, T10
- Touches: `Makefile`, `cmd/indexer/walk.go` (`--verify-slice`), this file (Overnight run procedure, Results)
- Details: `make summarize-run` = `go build -o bin/indexer ./cmd/indexer && caffeinate -i bin/indexer summarize --level function --concurrency 1`. Pause = `Ctrl-C` or `make summarize-pause` (`pkill -INT -f "bin/indexer summarize"`), which finishes the in-flight item and exits 0; resume = run `make summarize-run` again (rows `done` with unchanged `input_hash` are skipped). `make summarize-status` prints counts by status and an ETA from the last 100 completions. `indexer walk --verify-slice` (`make verify-slice`) recomputes the sha256 of the sorted `in_slice` path list and compares it with D-005. Quality gate BEFORE the overnight start: run `bin/indexer summarize --level function --limit 200`, print 30 seeded summaries (seed 3) with their code, and have the author mark each good or bad in Results; if more than 10% are bad, stop and revise `fn-v1` (new `prompt_version`) before launching. Then launch the run and record start time, expected end, and count remaining.
- Acceptance: `make verify-slice` exits 0, and exits non-zero after a temporary edit of the `slice.go` drop list; Results holds the 30-sample verdicts with `approved` from the author; `make summarize-run` starts, `make summarize-status` shows `done` growing; `make summarize-pause` exits the run within 60s; `make summarize-run` resumes without redoing done rows (done count never decreases); `pmset -g assertions | grep -i caffeinate` shows the assertion while running.
- Tests: none beyond T10.
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
- Throughput below 1k fn/h: the D-005 shrink order applies; do not start the run before T13.
- Mining yield below 30 candidates: T13 changes composition within the budget or extends the window; the slice never grows.
- Summary quality: the T14 gate (30 reviewed summaries) runs before the overnight start.
- Prompt truncation on huge functions: 6000-char cut plus the truncation check.
- Mac sleeps: `caffeinate -i`; keep on power.

## Demo check
`indexer walk && indexer parse --stats` prints stats; `psql` shows the qualified name `k8s.io/kubernetes/pkg/scheduler.(*Scheduler).schedulePod`; `make summarize-status` shows the run progressing.

## Results
(empty)
