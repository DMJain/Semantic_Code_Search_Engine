# S1 - Corpus & symbols

## Goal
Pin the Kubernetes tag, walk and filter the corpus, extract symbols, benchmark summarization and probe the mining yield to fix the slice, then launch the overnight function-summary run (the critical path).

## Entry criteria
S0 signed off locally (nothing is pushed, D-069): Ollama running with `qwen2.5-coder:7b` pulled, `make doctor` passes, `make test` shows 0 SKIP. `make ci-local` (S1-T15) replaces "CI green" for S1; CI proof on a pushed branch is an owner decision on the sign-off checklist (D-063). A fine-grained read-only `GITHUB_TOKEN` is in `.env` (needed from S1-T12; checked only with `test -n "$GITHUB_TOKEN"`, never printed, logged or committed).

## Links
[architecture.md#corpus-and-slice](../architecture.md#corpus-and-slice), [#stage-walk](../architecture.md#stage-walk), [#stage-parse](../architecture.md#stage-parse), [#stage-summarize](../architecture.md#stage-summarize), [#ollama-requests](../architecture.md#ollama-requests), [#token-estimate](../architecture.md#token-estimate), [#resumability](../architecture.md#resumability). Decisions: D-005, D-006, D-014, D-015, D-058, D-063 to D-068.

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
- Details: implement [#stage-walk](../architecture.md#stage-walk). Exclusion rules as a table of named predicates (`vendor`, `zz_generated`, `pb_go`, `testdata`, `test_go`, `client_go_generated` = paths under `staging/src/k8s.io/client-go/{kubernetes,listers,informers,applyconfigurations}` plus any file containing `Code generated` + `DO NOT EDIT` in the first 10 lines) and `build_constraint` = `go/build.Context{GOOS:"linux", GOARCH:"amd64", CgoEnabled:false}.MatchFile` rejects the file (the same context as `callgraph-gt`, D-058). Count excluded files per rule. `sha` = sha256 of contents; `loc` = non-blank lines. Upsert in batches of 500 inside a transaction; delete rows whose path vanished. Flag `--dry-run` prints stats only. The exclusion predicates are exported as one function (`walk.Excluded`) that S1-T08 and S1-T12 call; nothing re-implements them.
- Acceptance: with `CGO_ENABLED=1` in the environment, the build context `walk` constructs has `CgoEnabled == false` and a file with `import "C"` is excluded under `build_constraint` (D-063); `go run ./cmd/indexer walk` prints per-rule excluded counts, total indexed files, slice files, LOC, and exits 0; running it twice reports `0 changed`; `psql -c "select count(*) from files where path like 'vendor/%' or path like '%_test.go' or path like '%zz_generated%' or path like '%.pb.go' or path like '%/testdata/%'"` returns 0; `select count(*) from files where in_slice` > 0 and every in-slice path starts with a slice prefix; `select count(*) from files where path ~ '_(windows|darwin|freebsd|plan9)\.go$'` returns 0.
- Tests: unit test table for each exclusion predicate (including a `_windows.go` file and a `//go:build windows` file as negatives) with positive/negative paths; golden test over a small fixture tree; integration test for idempotency (uses `internal/testdb`, no raw `t.Skip`); an env test that `CGO_ENABLED=1` does not change the context.
- Out of scope: parsing.
- Owner: agent

### S1-T04 - tree-sitter setup and function/method extraction
- Why: symbols are the base of chunks, edges and summaries.
- Depends on: T03, T15
- Touches: `go.mod`, `internal/parse/`
- Details: add `github.com/smacker/go-tree-sitter` with the Go grammar. It needs CGO; `CGO_ENABLED=1` for building and testing is set in `ci.yml`, `make ci-local` and the Makefile build by T15 and is separate from the corpus build context (D-063). Confirm `go build ./...` and `make ci-local` pass. `parse.File(path, src) ([]Symbol, error)`: for `function_declaration` and `method_declaration` produce `Symbol{Kind: func|method, Name, QualifiedName, Receiver, Signature, StartLine, EndLine, Doc}`. Qualified name: `<import path>.<Name>` for functions, `<import path>.(*T).Name` / `.(T).Name` for methods (pointer vs value receiver preserved). Doc = contiguous `//` comment lines immediately above.
- Acceptance: `go test -count=1 ./internal/parse/...` passes (no golden file is rewritten by `make test` or CI; `-update` is manual only); `make ci-local` exits 0; golden file test over `testdata/sample.go` (committed fixture) yields exactly the expected JSON; qualified name of `schedulePod` in the real tree is `k8s.io/kubernetes/pkg/scheduler.(*Scheduler).schedulePod`.
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
- Details: select `files` where `parsed_sha IS DISTINCT FROM sha`; parse each file; packages with any stale file are re-parsed completely: compute qualified names for the whole package first, where duplicates (several `init`, same-named functions in different files of one package) get the suffix `#<n>` from the second occurrence ordered by `(path, start_line)`; then, in one transaction per file: `INSERT ... ON CONFLICT (qualified_name) DO UPDATE` (file_id, lines, signature, doc, `raw_calls`) `RETURNING id`, so a symbol keeps its `id` across re-parses (D-065); `DELETE FROM symbols WHERE file_id = $1 AND id <> ALL($seen) RETURNING id`; delete the function-level `summaries` whose `target_id` is in the deleted ids; then set `parsed_sha = sha`. Summary freshness is decided by recomputing `input_hash` from the CURRENT symbol and comparing it with the stored one, never by `target_id` alone (a duplicate-suffix shift, for example `init#1`/`init#2`, changes which code a name points at). `make reset-db` is never used while summaries are being kept. Flags: `--limit N`, `--stats`. SIGINT finishes the current file and exits 0. Prints files parsed, symbols by kind, parse errors (files with tree-sitter ERROR nodes are still stored, count printed).
- Acceptance: `indexer parse` then `indexer parse` again prints `0 files to parse`; killing the first run mid-way (`kill -INT`) then rerunning completes with total symbols equal to an uninterrupted run (compare `select count(*) from symbols`); `select count(*) from symbols s join files f on f.id=s.file_id where f.in_slice` > 0; `select qualified_name,count(*) from symbols group by 1 having count(*)>1` returns no rows.
- Tests: integration tests (`internal/testdb`) with a 3-file fixture tree: idempotency and resume; a symbol's `id` is unchanged after a re-parse with `parsed_sha` cleared; a function removed from a file leaves no symbol and no function summary for it, and the orphan query (`select count(*) from summaries s where s.level='function' and not exists (select 1 from symbols y where y.id=s.target_id)`) returns 0; an extra `init` inserted so that `init#1`/`init#2` shift makes the affected summaries report stale (recomputed `input_hash` differs); unit test for the cross-file `#n` suffix numbering (two files, same package, two `init` functions each).
- Out of scope: edges.
- Owner: agent

### S1-T08 - Full walk + parse run, audit and walk/go-packages equality
- Why: verify symbol quality on the real corpus, and prove before the freeze that `walk` and `go/packages` see the same files (D-058, D-067); a mismatch found after T13 could not be fixed.
- Depends on: T07
- Touches: `cmd/callgraph-gt/` (`--check` only), `go.mod` (`golang.org/x/tools`), this file (Results)
- Details: run walk and parse on the pinned tag. Record counts: files, slice files, LOC, symbols by kind, in-slice functions, in-slice functions with body >= 3 lines. Sample 30 random in-slice symbols (seed 42) and verify them with a script that re-reads the source at `start_line..end_line` and checks that the first line contains the symbol name and the receiver type and that the doc comment text equals the stored doc; record and explain mismatches. Equality check: `go run ./cmd/callgraph-gt --check` loads the four slice roots with `go/packages` (`NeedName|NeedFiles`, `Tests: false`, environment `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off GOTOOLCHAIN=local` with `CGO_ENABLED=0` appended so an inherited value never wins, `-mod=vendor`), applies the shared `walk.Excluded` predicate to the loaded `GoFiles`, normalizes paths with `filepath.EvalSymlinks`, and compares with `select path from files where in_slice`. The client-go root is loaded by import path `k8s.io/client-go/...` from the root module (staging has its own `go.mod` and no `vendor`); first confirm with `ls -l` that `vendor/k8s.io/client-go` is a symlink at the pinned tag. Packages whose files are all rejected by build constraints are empty on both sides. Exit 0 = equal, 1 = unequal (both directions listed, capped at 50 lines each, with totals), 2 = load error (record the exact failure under Q-05 and stop for the owner; do not change exclusion rules unprompted). Exclusion counts from both sides are printed. S2-T06 extends this same flag with offline loadability (D-025).
- Acceptance: Results contains the counts and the 30-sample table with 0 unexplained mismatches (mismatches explained or fixed); parse error count recorded; `go run ./cmd/callgraph-gt --check` exits 0 on the real tag and prints `file-set: equal (N files)`, or exits 2 with the failure recorded and the owner informed.
- Tests: on a small committed module fixture (with a `_windows.go` file, a `//go:build windows` file and an `import "C"` file): the walk set equals the `go/packages` set, run with `CGO_ENABLED=1` in the environment; the environment passed to `go/packages` ends with `CGO_ENABLED=0` whatever the process environment holds.
- Out of scope: fixes beyond parse bugs found; offline loadability and the VTA graph (S2).
- Owner: agent

### S1-T09 - Function summary prompt, Ollama call and request guards
- Why: the summarizer is the critical-path component (D-014); this task is its pure part (no database).
- Depends on: T06, T07
- Touches: `internal/summarize/prompt.go`, `internal/summarize/ollama.go`, `internal/summarize/guard.go`
- Details: `prompt_version = "fn-v1"`, defined once as the constant `summarize.PromptVersion`; `summarize-status`, embedding (S3, S5) and coarse-to-fine retrieval all filter on it (D-068). System prompt: "You are summarizing a Go function from Kubernetes for a code search index. Write 1-2 sentences describing what it does and why. No code, no preamble." User message: signature, doc, `Calls (raw): <raw_calls joined by ", ">`, then the code read from `KUBE_DIR` by the symbol's file and line range (cut to 6000 chars, marker `// ...truncated`). Ollama `/api/chat`, temperature 0, `num_predict` 120, explicit `num_ctx` from config (`OLLAMA_SUMMARY_NUM_CTX`, default 4096). `input_hash = sha256(prompt_version | code | signature | doc | raw_calls)`. Selection: in-slice function/method symbols with `end_line - start_line >= 3` (D-014). Guards per [#ollama-requests](../architecture.md#ollama-requests): pre-send, estimated tokens > 0.8 x `num_ctx` -> not sent, reason `too_large`; post-send, `prompt_eval_count >= num_ctx - num_predict` -> reason `truncated`. Other failure reasons: `http_error`, `empty_output`.
- Acceptance: `grep -rn '"fn-v' internal/ cmd/` shows only the constant; `go test -count=1 ./internal/summarize/...` passes with: golden prompt; `num_ctx` present in every request body; stable `input_hash`; selection test (a symbol with `end_line - start_line = 2` is excluded, `= 3` is included); a request estimated at 0.81 x `num_ctx` is not sent (the `httptest.Server` records 0 requests) and returns `too_large`; a response with `prompt_eval_count = num_ctx - num_predict` returns `truncated`; a normal response returns the text.
- Tests: the cases above plus a guarded live test with `OLLAMA_URL`.
- Out of scope: database state and CLI (T10); file/package levels (S5).
- Owner: agent

### S1-T10 - `indexer summarize` command: state, resume, flags
- Why: idempotent, resumable stage wiring for the critical-path run (D-014).
- Depends on: T09, T02
- Touches: `cmd/indexer/summarize.go`, `internal/summarize/run.go`
- Details: for each selected symbol upsert the `summaries` row on `summaries_uniq (level, target_id, prompt_version)` with `model` = the summary model, states `pending` (row created), `done`, `failed` (reason in `summaries.reason`, `text` stays NULL). Skip rows that are `done` with the same `input_hash`; re-pick `pending` and (with `--retry-failed`) `failed` rows. Order by symbol id. Flags: `--level function`, `--limit N`, `--sample N --seed S` (seeded draw stratified across the four slice roots in proportion to their eligible-function counts, candidates ordered by `qualified_name` before the seeded shuffle; the same seed gives a byte-identical id list), `--concurrency 1|2`, `--retry-failed`. SIGINT/SIGTERM finish the in-flight item, commit and exit 0.
- Acceptance: `indexer summarize --level function --limit 20` creates 20 `done` rows (`select status,count(*) from summaries group by 1`); rerunning makes 0 new Ollama calls; `select level,target_id,prompt_version,count(*) from summaries group by 1,2,3 having count(*)>1` returns no rows; a SIGINT mid-run leaves no partial row (every row is `done`, `failed`, or `pending` and re-picked on resume), the resume completes, and there are 0 duplicate `(level, target_id, prompt_version)` rows; the same `--sample 200 --seed 3` twice prints byte-identical id lists; a symbol whose estimate exceeds 0.8 x `num_ctx` ends as `failed` with reason `too_large` and no HTTP call.
- Tests: integration tests (`internal/testdb`) against `httptest.Server` (idempotency, resume, `too_large` row, a rerun makes 0 HTTP calls); sampling determinism and stratification unit tests; concurrency 2 test with `-race`.
- Out of scope: file/package levels and embedding summaries (S5).
- Owner: agent

### S1-T11 - Summarization throughput benchmark
- Why: the slice size is decided from measured throughput, not guesses (D-005).
- Depends on: T10
- Touches: `cmd/indexer/summarize.go` (`--bench`), this file (Results)
- Details: `indexer summarize --bench` samples 200 in-slice functions from the post-skip population (`end_line - start_line >= 3`) with seed 7, runs the exact `fn-v1` production prompt with the chosen concurrency (run 1 then 2), and prints functions per hour, p50/p95 latency, output tokens. Then times 20 file-level prompts and 3 package-level prompts with throwaway draft prompts (`file-bench`, `pkg-bench`): the 20 files are stratified by eligible-function count (seed 7); first summarize ALL eligible functions of those files with `fn-v1` (not stored) so the file prompt inputs have realistic length. Results go to `eval/results/bench/summarize.json`, not to the `summaries` table. Also record the count of skipped functions in the slice.
- Acceptance: `eval/results/bench/summarize.json` exists with fields `fn_per_hour_c1`, `fn_per_hour_c2`, `file_per_hour`, `pkg_per_hour`, `skipped_fns`, `slice_fns_eligible`; the command does not insert rows into `summaries`; the 20 files cover at least 3 distinct eligible-function-count strata (fields `file_strata`).
- Tests: unit test for seeded sampling determinism (same seed -> same IDs) and stratification (reuses the sampler of T10).
- Out of scope: tuning the prompt for quality beyond one sanity read of 10 outputs.
- Owner: agent

### S1-T12 - Mining-yield probe on the candidate slice
- Why: the slice is frozen forever at T13 and the 25 semantic queries must be minable inside it (D-005).
- Depends on: T01, T03 (and the token gate)
- Touches: `cmd/eval/mineyield.go`, `internal/config/config.go`, `.env.example`, `.gitignore`
- Details: add `GITHUB_TOKEN` to config and `.env.example` (empty, commented, never committed). `eval mine-yield [--window-months 12]` queries GitHub REST search in weekly `merged:` windows with `base:master` and `linked:issue` (search returns at most 1,000 results per query: a window that hits the cap is split in halves automatically; the search rate limit is read from `GET /rate_limit` (`resources.search`), not hard-coded, and the client waits until the reset time when remaining is 0, using an injected clock in tests); windows end at the pinned tag's date (T01); raw responses are cached under `eval/candidates/cache/` keyed by `(query, window)` (add to `.gitignore`), so S4-T05/T06 reuse them. For each PR fetch the changed files and count PRs whose changed non-test non-generated files (walk predicates including `build_constraint`) number 1-5, all inside the CANDIDATE slice (current `slice.go`), all existing at the pinned tag. No leakage filter (S4-T06). Report the 12-month yield and the 18-month extension yield (`--window-months 18`; the extra PRs between 12 and 18 months), counted after every automatable filter, and the contribution per slice prefix; write `eval/results/bench/mine-yield.json`. Without a token the command fails before any network call, and the token never appears in logs or output. S4-T05 reuses this HTTP client. Without a token: exit non-zero with `set GITHUB_TOKEN (read-only, public repos) in .env`.
- Acceptance: unset token -> exact message above and non-zero exit; with the token, `go run ./cmd/eval mine-yield` writes JSON with `yield_12m`, `yield_18m_extension`, `by_prefix` and `windows_at_cap` = 0 (capped windows are split until none remains); a second run makes 0 HTTP calls; the numbers are recorded in the T13 entry.
- Tests: `httptest.Server` for pagination, cap splitting, the rate limit read from `/rate_limit` and the wait (injected clock, no real sleeps); the token is absent from captured logs.
- Out of scope: leakage filtering and query choice (S4); changing the slice (T13).
- Owner: agent

### S1-T13 - [human] Decide and freeze the slice
- Why: the slice is the eval scope and must be final (D-005, D-064).
- Depends on: T08, T11, T12
- Touches: `internal/walk/slice.go`, `internal/walk/slice.sha256`, `docs/plan/decisions.md`
- Details: (1) Size: project the combined run length `T = eligible_fns/fn_per_hour + files/file_per_hour + packages/pkg_per_hour`. Apply the D-005 rule (at most 2 overnight runs, 20% safety margin); if too big, add subpath prefixes to the drop list in the D-005 order, re-run `walk`, recompute. (2) Yield: read `mine-yield.json`; the 12-month yield plus the 18-month extension yield must be at least 30. If short, never grow the slice beyond the compute budget; in order: (a) change the composition within the same budget (for example keep client-go and drop more pkg/controller subpackages), then re-run `walk`, `mine-yield` and the projection; (b) extend the mining window further back (`--window-months 24`, then 30). The KEP cap (5 of 25) stays. (2b) Re-run `go run ./cmd/callgraph-gt --check` on the post-drop-list set; it must exit 0 before freezing, and the 12-month and 18-month extension yields are recomputed on the frozen slice from the request cache (0 HTTP calls). (3) D-005 is append-only: do not edit it; append a new entry "Slice frozen (refs D-005)" with the tag and commit sha, the final size and calculation, the eligible-function count, the ordered drop list, both yields recomputed on the frozen slice, the `--check` exit code, and the sha256 of the sorted `in_slice` path list (repo-root-relative paths, `/` separators, one per line, LF-terminated, bytewise sort, the same as `LC_ALL=C sort`). Write the hash also to `internal/walk/slice.sha256`, which `make verify-slice` reads. State "FINAL, never changes"; a re-freeze needs an owner-authorized superseding entry. There is no CI hash pin.
- Acceptance: the "Slice frozen" entry exists with every field above; `make verify-slice` exits 0 and its hash equals the shell recomputation over the same sorted list; `select count(*) from symbols s join files f on f.id=s.file_id where f.in_slice and s.kind in ('func','method')` is between 15,000 and 25,000 (or the deviation is justified in the entry); the entry holds `yield_12m + yield_18m_extension >= 30` (or the window extension used); path-list sha256 recorded.
- Tests: none.
- Out of scope: later slice changes (forbidden).
- Owner: human

### S1-T14 - Overnight-run tooling, quality gate and launch
- Why: summaries are the critical path; the run must be pausable, sleep-proof and checked for quality before it consumes the compute budget.
- Depends on: T13, T10
- Touches: `Makefile`, `cmd/indexer/walk.go` (`--verify-slice`), `cmd/indexer/summarize.go` (`--status`), this file (Overnight run procedure, Results), `.scratch/S1/quality-gate-<prompt_version>.jsonl`
- Details: `make summarize-run` = `go build -o bin/indexer ./cmd/indexer && caffeinate -i bin/indexer summarize --level function --concurrency 1`. Pause = `Ctrl-C` or `make summarize-pause` (`pkill -INT -f "bin/indexer summarize"`), which finishes the in-flight item and exits 0; resume = run `make summarize-run` again (rows `done` with unchanged `input_hash` are skipped). `make summarize-status` prints counts by status for the CURRENT `prompt_version` only (D-068) and an ETA from the last 100 completions. `indexer walk --verify-slice` (`make verify-slice`) recomputes the sha256 of the sorted `in_slice` path list and compares it with `internal/walk/slice.sha256`.
  Quality gate BEFORE the overnight start: run `bin/indexer summarize --level function --sample 200 --seed 3` (stratified across the four roots). Report the `too_large` and `truncated` counts among the 200 separately. Draw 30 summaries (seed 3) from the `done` gate rows and print each with its code. The author marks each good or bad; a summary is bad if it states behavior the code lacks, names a callee or behavior that is absent, restates the signature, contains code or preamble, omits the primary effect or return value, or is empty or cut off; record the first failing criterion. Verdicts go to `.scratch/S1/quality-gate-<prompt_version>.jsonl` as `{qualified_name, input_hash, verdict, criterion}`. At most 3 bad passes; 4 or more bad fails the gate: revise the prompt, bump `PromptVersion`, redo the gate; a second failure stops the agent for the owner. When the gate passes, the 200 gate rows stay (the full run skips them by `input_hash`). If a prompt version was bumped, delete the failed version's function-level rows at this point, before any summary embedding.
  The agent never starts the full run: the owner runs `make summarize-run` in their own terminal. The agent then records the start, the expected end and the count remaining.
- Acceptance: `make verify-slice` exits 0, and exits non-zero after a temporary edit of the `slice.go` drop list; Results holds the 30 verdicts with `approved` from the author and at most 3 bad; `select distinct prompt_version from summaries where level='function'` returns only the current one; `make summarize-run` (started by the owner) shows `done` growing across two `make summarize-status` readings (strictly increasing); pause and resume are verified by counts: start the run, wait until `done >= start + 5`, send SIGINT, assert no partial rows, resume, `done` increases and there are 0 duplicate `(level, target_id, prompt_version)` rows; `pmset -g assertions | grep -i caffeinate` shows the assertion while running.
- Tests: none beyond T10.
- Out of scope: file/package levels; supervising the full run to completion.
- Owner: agent, with the gate verdicts and the run start by the author

### S1-T15 - `make ci-local` and CGO-safe CI (new)
- Why: CI is never observed remotely (D-069); a local mirror of `ci.yml` is the substitute, and tree-sitter needs CGO for building while the corpus build context stays `CGO_ENABLED=0` (D-063).
- Depends on: none (blocks T04 only)
- Touches: `Makefile`, `.github/workflows/ci.yml`
- Details: `make ci-local` runs the commands and environment of `ci.yml` against the compose Postgres: `go vet ./...`, `REQUIRE_TEST_DB=1 go test -v -count=1 ./...` with the SKIP check, `make lint`, and `make lint-ci`. `make lint-ci` = `GOTOOLCHAIN=local go run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION) $(WORKFLOW)` with `WORKFLOW ?= .github/workflows/ci.yml`; choose the version from `go list -m -versions github.com/rhysd/actionlint`, and if the newest does not build under `GOTOOLCHAIN=local`, take the newest that does and record why. actionlint's shellcheck integration is skipped when `shellcheck` is not on PATH; state that in Results. Set `CGO_ENABLED=1` on the build, vet and test steps of `ci.yml`, in `make ci-local` and in the Makefile `build` target (not job-wide, and never in the walk or `callgraph-gt` code paths).
- Acceptance: `make ci-local` exits 0 with 0 SKIP; `make lint-ci` exits 0, and exits non-zero on a scratch copy of `ci.yml` containing a typo (`make lint-ci WORKFLOW=<scratch copy>`); `ci.yml` and `make ci-local` run the same commands (diff recorded in Results).
- Tests: none.
- Out of scope: pushing, Ollama in CI.
- Owner: agent

## Overnight run procedure
This is the single source for the run; later sprints link here.
1. Start: `make summarize-run`. Use it overnight with the lid open on power. Check: `make summarize-status`.
2. Pause before any heavy Ollama job (embedding runs, S3-T09; `ollama` model swaps thrash memory): `make summarize-pause`, wait for exit.
3. Resume: `make summarize-run` (idempotent).
4. Done when `make summarize-status` shows 0 pending and failed < 2% of eligible; run `summarize --level function --retry-failed` once for failures.
The agent does not start the full run; the owner does (D-064). S1 does not wait for the run to finish.

## Measurements to record
Written to this file's Results and `eval/results/bench/summarize.json`:
- Quality-gate verdict counts and the `too_large`/`truncated` counts among the 200 gate rows.
- Walk stats (files, slice files, LOC, excluded per rule), symbol counts by kind, parse errors.
- Benchmark: functions/hour (c1, c2), file/package prompts per hour, skipped-function count.
- Final slice size and projected combined summarization run length (D-005); run start timestamp.
- Indexing metrics for `walk` and `parse` wall time ([eval-protocol.md#indexing-metrics](../eval-protocol.md#indexing-metrics)).

## Exit criteria / Definition of Done
Tag pinned (D-006); walk and parse idempotent; `callgraph-gt --check` equal; slice FINAL with its hash recorded at T13; quality gate passed; the owner has started the function-summary run and two `summarize-status` readings show `done` strictly increasing; pause and resume verified by counts; `make ci-local` green with 0 SKIP; ROADMAP updated. CI is checked only by `make ci-local` (nothing is pushed, D-069).

## Risks + mitigations
- CGO/tree-sitter build issues on CI: `CGO_ENABLED=1` is set in T15 and exercised locally by `make ci-local` in T04; CI is never observed remotely (D-069). Fallback is `gotreesitter` bindings, not Python.
- `go/packages` cannot load client-go from the clone's `vendor/` layout (Q-05): T08 exits 2, records the failure and stops for the owner before the freeze.
- Throughput below 1k fn/h: the D-005 shrink order applies; do not start the run before T13.
- Symbol ids changing under existing summaries: T07 upserts on `qualified_name` and cleans up per file (D-065).
- Mining yield below 30 candidates (12-month plus 18-month extension): T13 changes composition within the budget or extends the window; the slice never grows.
- Summary quality: the T14 gate (30 reviewed summaries, at most 3 bad) runs before the overnight start.
- Prompt truncation on huge functions: 6000-char cut plus the truncation check.
- Mac sleeps: `caffeinate -i`; keep on power.

## Demo check
`indexer walk && indexer parse --stats` prints stats; `psql` shows the qualified name `k8s.io/kubernetes/pkg/scheduler.(*Scheduler).schedulePod`; `make summarize-status` shows the run progressing.

## Results

### S1-01 / T15 - `make ci-local` and CGO-safe CI

**Pinned actionlint version: `v1.7.12`.** Chosen from `GOTOOLCHAIN=local go list -m -versions github.com/rhysd/actionlint` (newest entry). The absolute newest required no fallback: `v1.7.12` builds and runs under `GOTOOLCHAIN=local` with the local toolchain (`GOTOOLCHAIN=local go run ...@v1.7.12 -version` reports `v1.7.12` "built with go1.27.1 compiler for darwin/arm64"), so the newest that works *is* the newest. Pinned as the Makefile variable `ACTIONLINT_VERSION`, next to `GOLANGCI_LINT_VERSION`.

**shellcheck check runs (not skipped).** `shellcheck 0.11.0` is on `PATH`, so actionlint's shell-script integration is active. Proven with a scratch workflow whose only defect was `run: echo $FOO`; `make lint-ci WORKFLOW=/tmp/s1-shellcheck.yml` reported `SC2086:info: ... Double quote to prevent globbing [shellcheck]` and exited non-zero. actionlint auto-detects shellcheck; when it is absent the shell-script checks are silently skipped (the ticket's note). The GitHub `ubuntu-latest` runner ships shellcheck, so CI and `ci-local` behave the same.

**Commands in `ci.yml` vs `make ci-local`:**

| Step | `ci.yml` | `make ci-local` |
|---|---|---|
| Vet | `go vet ./...`, per-step `env: CGO_ENABLED: "1"` | `CGO_ENABLED=1 go vet ./...` |
| Test | `make test`, per-step `env: CGO_ENABLED: "1"` | `CGO_ENABLED=1 make test` |
| Lint | `golangci/golangci-lint-action@v9` `version: v2.14.0` then `make lint-ci` | `make lint` (local binary) then `make lint-ci` |

Differences, all explained:
- **golangci-lint**: CI invokes the `golangci/golangci-lint-action@v9` wrapper pinned to `v2.14.0`; `ci-local` invokes the locally installed `golangci-lint` binary (`v2.14.0`, same pin). The commands are the same (`golangci-lint run`); only the delivery mechanism differs. `make lint` also fails fast with an install hint if the binary is missing.
- **Platform / C toolchain**: CI runs on `ubuntu-latest` (system clang); `ci-local` runs on macOS `darwin/arm64` (Xcode CommandLineTools clang). `CGO_ENABLED=1` is set per-step in `ci.yml` (never job-wide) and inline in `ci-local`; it is what lets the later tree-sitter parser (T04) build. The corpus build context stays `CGO_ENABLED=0` (D-063), untouched here.
- **Postgres**: CI provisions a `pgvector/pgvector:pg17` service container; `ci-local` uses the compose Postgres via `TEST_DATABASE_URL` from `.env` and additionally refuses early when it is unset (the guard inside `make test`).
- The vet command, the test command (`make test` → `REQUIRE_TEST_DB=1 go test -count=1 -v ./...` with the SKIP check) and the actionlint command (`make lint-ci`) are literally the same strings in both.

**Acceptance evidence (trimmed):**

```
$ make ci-local
CGO_ENABLED=1 go vet ./...
CGO_ENABLED=1 /Library/Developer/CommandLineTools/usr/bin/make test
...
ok  github.com/DMJain/Semantic_Code_Search_Engine/internal/walk  1.330s
/Library/Developer/CommandLineTools/usr/bin/make lint
golangci-lint run
0 issues.
/Library/Developer/CommandLineTools/usr/bin/make lint-ci
GOTOOLCHAIN=local go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/ci.yml
$ echo $?  ->  0        # 0 `--- SKIP`, no FAIL
```

```
$ make lint-ci
GOTOOLCHAIN=local go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/ci.yml
$ echo $?  ->  0
```

```
$ make lint-ci WORKFLOW=/tmp/s1-ci-typo.yml   # scratch copy with `runs-ON` typo
/tmp/s1-ci-typo.yml:11:3: "runs-on" section is missing in job "test" [syntax-check]
/tmp/s1-ci-typo.yml:12:5: unexpected key "runs-ON" for "job" section ... [syntax-check]
exit status 1
$ echo $?  ->  2
```
Scratch copies (`/tmp/s1-ci-typo.yml`, `/tmp/s1-shellcheck.yml`) were deleted after the run.

### S1-02 / T01 - Pin and fetch the Kubernetes tag

**Chosen tag: `v1.37.0`**, commit `f54c212e3a2f75d674b717a9b29052b20b60aefc`, committed 2026-08-26, `go` directive `1.26.0` (`<=` local go1.27.1). Latest stable `v1.N.0` with no alpha/beta/rc whose go directive is not newer than the local toolchain; recorded in D-006. `KUBE_TAG=v1.37.0` in `.env` and `.env.example`.

```
$ git -C data/kubernetes describe --tags          -> v1.37.0
$ test -d data/kubernetes/vendor && test -d data/kubernetes/staging/src/k8s.io/client-go   -> OK
$ printf '%s\n%s\n' "$(grep '^go ' data/kubernetes/go.mod | cut -d' ' -f2)" \
      "$(go env GOVERSION | sed 's/^go//')" | sort -V -C                       -> OK (1.26.0 <= 1.27.1)
$ make fetch-repo   # second run
level=INFO msg="kubernetes already checked out at tag, skipping" dir=./data/kubernetes tag=v1.37.0   -> exit 0
```

Clone fact for Q-05: `vendor/k8s.io/` holds only gengo, klog, kube-openapi, system-validators and utils — no `client-go`/`api`/`apimachinery` — and the clone carries `go.work` (`use ./staging/src/k8s.io/...`), so the `GOWORK=off -mod=vendor` invocation assumed by D-058 cannot resolve client-go from `vendor/`.

### S1-04 / T03 - `indexer walk`

Real run over `data/kubernetes` (v1.37.0), one exported rule table (`walk.Excluded`, shared with S1-08/S1-12):

```
excluded vendor               4460
excluded zz_generated         1221
excluded pb_go                 119
excluded testdata               50
excluded test_go              3195
excluded client_go_generated  2021
excluded build_constraint      176
indexed 6633 files (slice 1131), LOC 1145424 (slice 234075)
run 1: 6633 changed, 0 deleted
run 2: 0 changed, 0 deleted        # idempotent
```

Notes:
- `build_constraint` adds an explicit `importsC(abs) && !CgoEnabled` check: `go/build.Context.MatchFile` does NOT reject `import "C"` (verified on go1.27.1), so without it a cgo file would be indexed. The walk's context is always `{linux, amd64, CgoEnabled:false}` whatever `CGO_ENABLED` the shell sets.
- The sprint's literal DB check `path like '%_test.go'` is wrong: `_` is a LIKE single-char wildcard and it matches 8 legitimate names (`latest.go`, `resttest.go`, …). The rule is a `_test.go` suffix; the escaped check `path like '%\_test.go' escape '\'` and `path ~ '_test\.go$'` both return 0.
- `in_slice` = 1131, all with a slice prefix; `_(windows|darwin|freebsd|plan9)\.go$` count 0.
- `make test` 0 SKIP; `go vet ./...`, `make lint`, `make ci-local` clean.

### S1-05 / T04+T07 - `indexer parse` (functions and methods)

Tree-sitter Go grammar (`github.com/smacker/go-tree-sitter`, CGO) extracts `function_declaration` -> `func` and `method_declaration` -> `method`; qualified name `<import path>.<Name>` / `<import path>.(*T).Name` (pointer vs value receiver preserved), signature, line range and contiguous `//` doc.

Real run over `data/kubernetes` (v1.37.0), after `indexer walk`:

```
parse = 6633 files, 44539 symbols (func 19796, method 24743), 15 parse errors
schedulePod -> k8s.io/kubernetes/pkg/scheduler.(*Scheduler).schedulePod (lines 572-626)
second parse -> 0 files to parse
select qualified_name,count(*) ... having count(*)>1 -> 0 rows
```

Interrupt/resume: `kill -INT` exited 0 after writing complete files; resume parsed the rest; final `count(*) from symbols` = 44539, equal to the uninterrupted run, 0 duplicates.

Integration tests (throwaway DBs, no raw `t.Skip`): idempotency; a symbol id is unchanged after a re-parse with `parsed_sha` cleared; a removed function leaves no symbol and no function summary and the orphan query returns 0; an inserted extra `init` shifts `init#n` so the stored summary's recomputed `input_hash` differs (stale). `#n` numbering is the second occurrence onward (`init`, `init#1`, `init#2`, ...), ordered by `(path, start_line)`.

`make ci-local` exits 0 (parser compiles with CGO); `make test` 0 SKIP; `go vet ./...`, `make lint` clean.

### S1-06 / T05 - struct and interface symbols

Struct and interface types are extracted as symbols with a header-only signature (`type Name` + type-parameter list + `struct`/`interface`, capped at 200 chars, no field bodies), doc comment and qualified name `<import path>.Name`. Grouped `type ( ... )` blocks yield one symbol per spec; `type X = Y` aliases and non-struct/interface named types are skipped.

Real tree (after clearing `parsed_sha` and re-parsing): `parsed 6633 files, 55499 symbols (func 19796, method 24743, struct 9267, interface 1693), 15 parse errors`; second run `0 files to parse`; duplicate qualified names 0. Goldens cover grouped blocks, generic types and embedded interfaces. `make test` 0 SKIP; `make ci-local` exits 0; `go vet`/`make lint` clean.

### S1-13 / T12 - Mining-yield probe

`eval mine-yield` (GitHub REST search, weekly `merged:` windows with `base:master linked:issue`, windows ending at the tag date 2026-08-26; a window at the 1,000-result cap is halved recursively). Candidate = merged PR closing an issue whose 1-5 changed non-test non-generated `.go` files are all in the candidate slice and exist at the tag (reusing `walk.Excluded`/`walk.InSlice`).

Real run (`eval/results/bench/mine-yield.json`, token never printed):

```
yield_12m = 128     yield_18m_extension = 40     (total 168 >= 30)
by_prefix = {controller 37, kubelet 67, scheduler 49, client-go 18}
windows_at_cap = 0  prs_scanned = 938
rerun: 0 HTTP calls (cache under eval/candidates/cache/, gitignored)
```

Deviations recorded: changed non-`.go` files are ignored (Go-only corpus), so a PR touching only non-Go files in the slice counts 0; "exists at the tag" is an `os.Stat` against the checkout, valid because `fetch-repo` is pinned to the tag; `by_prefix` counts a PR once per slice prefix it touches (overlaps, so the sum may exceed the yield). Fake-GitHub tests cover pagination, cap halving, the `/rate_limit` search bucket waited out on an injected clock (no real sleeps), cache reuse (second run 0 HTTP), the missing-token gate before any network call, and the token absent from captured logs.

### S1-07 / T06 - raw calls per function

Each function/method stores the call-expression function-operand text as written (trimmed, truncated to 80 runes, deduplicated in source order, capped at 40, nested `func_literal` calls included), written in the same transaction as the symbol. Builtins and predeclared-type conversions are dropped by one fixed list (`builtins`); a user-defined-type conversion `MyType(x)` is kept (syntactically indistinguishable without type info). On the real tree `schedulePod`'s raw_calls is non-empty (e.g. `utiltrace.New`, `sched.findNodesThatFitPod`, `framework.NewSortedScoredNodes`). Goldens cover method chains, builtins, closures and both dropped (`any`, `string`) and kept (`MyType`) conversions. A re-parse overwrites the stored list through the upsert. Raw calls are never written to `edges`.

### S1-08 / T08 first half - walk + parse audit

Clean full pass on the pinned tag `v1.37.0` (commit `f54c212e3a2f75d674b717a9b29052b20b60aefc`): `indexer walk` (idempotent), then `parsed_sha` cleared and `indexer parse --stats` re-run so every symbol and its `raw_calls` is rewritten from source (33421 symbols now carry a non-empty `raw_calls`).

Commands (env from `.env`; SQL via `docker compose exec -T postgres psql -U codesearch -d codesearch`, no local `psql`):

```
$ CGO_ENABLED=1 go run ./cmd/indexer walk
excluded vendor 4460, zz_generated 1221, pb_go 119, testdata 50, test_go 3195, client_go_generated 2021, build_constraint 176
indexed 6633 files (slice 1131), LOC 1145424 (slice 234075)
0 changed, 0 deleted
$ psql -c "UPDATE files SET parsed_sha = NULL"      ->  UPDATE 6633
$ CGO_ENABLED=1 go run ./cmd/indexer parse --stats   # 19.2s wall
6633 files to parse
parsed 6633 files, 55499 symbols, 15 parse errors
symbols by kind: func 19796, method 24743, struct 9267, interface 1693
```

Counts:

| metric | value |
|---|---|
| total files | 6633 |
| in-slice files | 1131 |
| LOC | 1,145,424 |
| in-slice LOC | 234,075 |
| symbols total | 55,499 |
| func | 19,796 |
| method | 24,743 |
| struct | 9,267 |
| interface | 1,693 |
| in-slice func/method | 10,908 |
| in-slice func/method with `end_line - start_line >= 3` | 8,225 |
| parse errors | 15 |

**Audit tool.** New `indexer audit` subcommand (`cmd/indexer/audit.go`, registered in `main.go`): selects in-slice `func`/`method` rows `ORDER BY qualified_name`, shuffles them with `math/rand.New(rand.NewSource(seed))` (seed 42, sample 30), re-reads `KUBE_DIR/<path>` lines `start_line..end_line`, and checks (1) the first line contains the symbol name, (2) the receiver type appears in the signature, (3) the stored `doc` equals the contiguous `//` lines immediately above `start_line`. Prints a table and exits non-zero on any mismatch. Unit tests (`cmd/indexer/audit_test.go`) cover the deterministic sampler (same seed -> same ids, different seed -> different order, clamping) and the pure helpers `checkSymbol`/`docAbove`.

```
$ CGO_ENABLED=1 go run ./cmd/indexer audit | tee /tmp/s1-08-audit.txt
audit sample: 30 in-slice func/method symbols (seed 42)
...+ 30 table rows, all "ok"...
30/30 ok       # exit 0
$ shasum -a 256 /tmp/s1-08-audit.txt
474e03beded3b7e2effcf23e2c7ac224e6dc71548bfc1fc1bc481f41fe9bd3f2
```

A second run produced the identical sha256 (reproducible). 30-sample table (seed 42), all `ok`, **0 unexplained mismatches**:

| qualified_name | result |
|---|---|
| k8s.io/client-go/discovery.(\*DiscoveryClient).ServerGroupsAndResourcesWithContext | ok |
| k8s.io/client-go/util/workqueue.(\*waitForPriorityQueue[T]).Push | ok |
| k8s.io/kubernetes/pkg/kubelet.(\*ReasonCache).add | ok |
| k8s.io/kubernetes/pkg/controller/volume/attachdetach/cache.(\*actualStateOfWorld).AddVolumeNode | ok |
| k8s.io/kubernetes/pkg/kubelet/status.isInitContainerInitialized | ok |
| k8s.io/client-go/rest.(\*NoBackoff).CalculateBackoff | ok |
| k8s.io/kubernetes/pkg/kubelet/kuberuntime.convertToRuntimeSELinuxOption | ok |
| k8s.io/kubernetes/pkg/scheduler/backend/cache/debugger.(\*CacheDumper).dumpSchedulingQueue | ok |
| k8s.io/kubernetes/pkg/scheduler/framework/plugins/interpodaffinity.(\*InterPodAffinity).isSchedulableAfterNodeChange | ok |
| k8s.io/kubernetes/pkg/kubelet/cm/memorymanager.memoryBlocksToString | ok |
| k8s.io/client-go/tools/record.WithContext | ok |
| k8s.io/kubernetes/pkg/kubelet/allocation/state.(\*noopStateCheckpoint).RemovePod | ok |
| k8s.io/client-go/discovery/fake.(\*FakeDiscovery).ServerGroups | ok |
| k8s.io/kubernetes/pkg/controller/replication.(podControlAdapter).DeletePod | ok |
| k8s.io/client-go/transport.NewCachedTokenSource | ok |
| k8s.io/client-go/tools/cache.(\*sharedIndexInformer).handleBatchDeltas | ok |
| k8s.io/client-go/testing.NewGetSubresourceActionWithOptions | ok |
| k8s.io/client-go/transport.maskValue | ok |
| k8s.io/kubernetes/pkg/scheduler/testing/framework.Node2PrioritizerExtender | ok |
| k8s.io/kubernetes/pkg/controller/volume/attachdetach/cache.(\*actualStateOfWorld).DeleteVolumeNode | ok |
| k8s.io/kubernetes/pkg/scheduler/framework.(\*CycleState).ShouldRecordPluginMetrics | ok |
| k8s.io/client-go/tools/watch.(\*RetryWatcher).doReceive | ok |
| k8s.io/kubernetes/pkg/kubelet/container/testing.(\*MockRuntime).APIVersion | ok |
| k8s.io/kubernetes/pkg/scheduler/testing/framework.(PersistentVolumeLister).List | ok |
| k8s.io/kubernetes/pkg/kubelet/volumemanager.(\*volumeManager).GetMountedVolumesForPod | ok |
| k8s.io/kubernetes/pkg/controller/job.sortByFinishedTime | ok |
| k8s.io/client-go/tools/watch.UntilWithoutRetry | ok |
| k8s.io/client-go/tools/cache.(\*tombstone).GetName | ok |
| k8s.io/client-go/util/cert.GetServingCertificates | ok |
| k8s.io/kubernetes/pkg/kubelet/volumemanager/reconciler.(\*globalVolumeInfo).addPodVolume | ok |

**No parse bug found** (0/30 mismatches); no walk/parse logic changed. Tag `v1.37.0`; audit output sha256 `474e03beded3b7e2effcf23e2c7ac224e6dc71548bfc1fc1bc481f41fe9bd3f2`.

Acceptance: `go test -count=1 ./...` passes; `make test` 0 SKIP; `CGO_ENABLED=1 go vet ./...` clean; `make lint` 0 issues; `make ci-local` exits 0.

### S1-09 / T08 second half - walk vs go/packages file-set check

`callgraph-gt --check` loads the four slice roots with `go/packages` (`NeedName|NeedFiles`, `Tests:false`), applies the shared `walk.Excluded` predicate, normalizes symlinks and compares with the walk's `in_slice` set.

Real tag run: the pinned `GOWORK=off -mod=vendor` load fails (`inconsistent vendoring`, matching Q-05), so the workspace (`GOWORK=auto`, `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`, `CGO_ENABLED=0` appended last) is used — 720 packages, 0 load errors. Result:

```
load: workspace (GOWORK=auto)
walk excluded:     vendor 4460, zz_generated 1221, pb_go 119, testdata 50, test_go 3195, client_go_generated 2021, build_constraint 176
packages excluded: vendor 0, zz_generated 89, pb_go 4, testdata 0, test_go 0, client_go_generated 2021, build_constraint 1
file-set: equal (1131 files)     exit 0
vendor/k8s.io/client-go: absent at the tag (not a symlink)   exit 0
```

Q-05 updated with the outcome. Fixture test (with `_windows.go`, `//go:build windows` and `import "C"` files): walk set == go/packages set with `CGO_ENABLED=1` in the process env; the env builder always ends with `CGO_ENABLED=0`.

### S1-12 / T11 - Summarization throughput benchmark

`indexer summarize --bench` (new `--bench` flag and `--out`, default `eval/results/bench/summarize.json`) measures throughput **without writing any summary row**: it only reads `symbols`/`files`, and the fn-v1 calls for the file/package input-building pass are not stored either. Bench logic lives in `internal/summarize/bench.go` (pure `SampleFiles`, `FileStratum`, `WriteBenchResult`, and `Runner.Bench`); the CLI wiring is in `cmd/indexer/summarize.go`. `summarize.Result` now carries Ollama's `eval_count` as `OutputTokens` for the token metric.

Design: 200 eligible functions drawn with the ticket-11 `summarize.Sample` (stratified across the four slice roots, seed 7) run with the exact production fn-v1 prompt at concurrency 1, then the same 200 at concurrency 2. 20 files are drawn stratified by eligible-function-count (`FileStratum` buckets `1-2`, `3-5`, `6-11`, `12-23`, `24+`; largest-remainder allocation, seed 7); before a file prompt is timed, **all** its eligible functions are summarized with fn-v1 (not stored) and the file prompt is built from those summaries. 3 package prompts aggregate the file summaries of 3 distinct packages. The file- and package-level prompts are throwaway drafts (`file-bench`, `pkg-bench`) documented as synthetic in the JSON `note`, since hierarchical summaries are S5. `file_per_hour`/`pkg_per_hour` time only the file/package prompt itself, so the D-005 projection `T = eligible_fns/fn_per_hour + files/file_per_hour + packages/pkg_per_hour` does not double-count the constituent fn calls.

Real run (qwen2.5-coder:7b, `num_ctx` 4096, seed 7), wall **5105.5 s (~85 min)**, ~646 Ollama calls:

```
fn_per_hour_c1 = 459.1     fn_per_hour_c2 = 622.5
file_per_hour  = 167.3     pkg_per_hour   = 226.4
skipped_fns    = 2683      slice_fns_eligible = 8225
file_strata    = [1-2, 3-5, 6-11, 12-23, 24+]  (5 strata)
fn p50/p95 ms: c1 6897/15785, c2 11153/16990; output tokens fn 54, file 96, pkg 93
file_done 19/20 (one 68-eligible file exceeded the 0.8 x num_ctx pre-send guard -> too_large)
summaries rows before = 28, after = 28   (unchanged; --bench inserts nothing)
```

Files/package levels and embeddings remain out of scope (S5); no migration touched. Tests: `TestSampleFilesDeterministic` and `TestSampleFilesStrata` (seeded determinism and >= 3 strata on a fake candidate set) and `TestFileStratumBoundaries` reuse/extend the ticket-11 sampler tests; `TestBenchDoesNotInsertSummaries` runs the whole benchmark against a throwaway DB plus a fake Ollama server and asserts `summaries` stays empty.

Acceptance: `eval/results/bench/summarize.json` has `fn_per_hour_c1`, `fn_per_hour_c2`, `file_per_hour`, `pkg_per_hour`, `skipped_fns`, `slice_fns_eligible` and `file_strata` (5 strata >= 3); summaries row count 28 before and 28 after; `go test -count=1 ./...` passes; `make test` 0 SKIP; `go vet ./...` and `make lint` clean.

### S1-14 / T13 - Slice frozen

The author approved this exact freeze: apply the D-005 drop order and drop `staging/src/k8s.io/client-go` (the concurrency-1 eligible-function projection exceeded the D-005 budget), then freeze. Decision entry **D-070** appended after D-069; **D-005 is unedited**. One-shot freeze; no CI hash pin (D-069).

**Ordered drop list (D-005 order):** `["staging/src/k8s.io/client-go"]`.

**Counts (frozen slice):**

| metric | pre-drop | frozen |
|---|---|---|
| in-slice files | 1,131 | **876** |
| in-slice packages | 423 | **329** |
| in-slice LOC | 234,075 | **187,823** |
| in-slice func/method | 10,908 | **8,534** |
| in-slice func/method with `end_line - start_line >= 3` (eligible) | 8,225 | **6,673** |
| files with an eligible fn | — | 629 |
| packages with an eligible fn | — | 293 |

Total corpus unchanged: 6,633 files, 55,499 symbols (func 19,796, method 24,743, struct 9,267, interface 1,693).

**D-005 calculation.** Eligible-function budget = `min(25k, fn_per_hour_c1 × 20 h × 0.8) = min(25k, 459.14 × 20 × 0.8) = 7,346`. Candidate 8,225 > 7,346 -> first drop applied; frozen 6,673 <= 7,346. Combined run-length (T13 formula, `eval/results/bench/summarize.json`): function `6,673/459.14 = 14.5 h` + file `876/167.33 = 5.2 h` + package `329/226.35 = 1.5 h` = **21.2 h at concurrency 1** (17.4 h at c2); the S1 function-level overnight run is 14.5 h and fits the 16 h effective budget.

**File-set check.** `go run ./cmd/callgraph-gt --check` -> `file-set: equal (876 files)`, **exit 0** (workspace `GOWORK=auto` load, 0 load errors; walk excluded vendor 4460, zz_generated 1221, pb_go 119, testdata 50, test_go 3195, client_go_generated 2021, build_constraint 176).

**Yields recomputed on the frozen slice (0 HTTP calls, from `eval/candidates/cache/`).** `yield_12m = 115`, `yield_18m_extension = 35` (total **150 >= 30**); `by_prefix = {pkg/controller 35, pkg/kubelet 67, pkg/scheduler 49, staging/src/k8s.io/client-go 0}`; `windows_at_cap = 0`; `prs_scanned = 938`. A second immediate run produced identical numbers with the cache directory's file count (1,056) and mtimes unchanged -> **0 HTTP calls**.

**Frozen hash.** sha256 of the sorted `in_slice` path list (repo-root-relative, `/` separators, one per line, LF-terminated, bytewise sort):

```
2738a003140d98239b54254051a0ddf0b46c9fd1b554ac6f4f6f43edc3c04ec5
```

Written to `internal/walk/slice.sha256` (exactly 64 lowercase hex + one newline, 65 bytes). Shell recomputation over the same list:

```
$ docker compose exec -T postgres psql -tA -U codesearch -d codesearch \
    -c "select path from files where in_slice" | LC_ALL=C sort | shasum -a 256
2738a003140d98239b54254051a0ddf0b46c9fd1b554ac6f4f6f43edc3c04ec5  -
```

-> **match** (`internal/walk/slice.sha256` == shell). 876 paths, 0 duplicates.

**Deviation from the 15,000-25,000 in-slice func/method expectation.** Not met (8,534 total, 6,673 eligible), justified in D-070: at tag `v1.37.0` the four roots hold only ~8.5k non-generated `func`/`method` after the walk exclusions (vendor, `zz_generated`, `.pb.go`, `testdata`, `_test.go`, `client_go_generated`, build-constrained); reaching 15,000+ would require counting generated or test functions, which the slice's hand-written-code-search purpose forbids.

**Commands run** (env via `set -a; . ./.env; set +a`):

```
CGO_ENABLED=1 go run ./cmd/indexer walk          # 255 changed; indexed 6633 (slice 876), LOC 187823
go run ./cmd/callgraph-gt --check                # file-set: equal (876 files), exit 0
go run ./cmd/eval mine-yield --window-months 18  # yield_12m=115 yield_18m_extension=35 windows_at_cap=0
go run ./cmd/eval mine-yield --window-months 18  # rerun: 0 HTTP calls (cache unchanged)
docker compose exec -T postgres psql ... | LC_ALL=C sort | shasum -a 256   # frozen hash (above)
```

**Measurements to record (sprint).** Final slice size 876 files / 329 packages / 187,823 LOC; projected combined summarization run length 21.2 h at concurrency 1 (17.4 h at c2), function level alone 14.5 h.

**Acceptance.** D-070 holds every required field; D-005 unedited. `internal/walk/slice.sha256` equals the shell recomputation. `--check` exits 0. `yield_12m + yield_18m_extension = 150 >= 30`. Eligible count below 15,000 but deviation justified in D-070. `CGO_ENABLED=1 go test -count=1 ./...` passes; `CGO_ENABLED=1 make test` exits 0 with 0 `--- SKIP`; `CGO_ENABLED=1 go vet ./...` clean; `make lint` -> `0 issues.`. Fixture updates: `internal/walk/walk_test.go` `TestWalkGoldenFixture` now expects `InSlice = 1` (the fixture's client-go file stays indexed but is no longer in the slice); `slice_test.go` pins the frozen drop list and the client-go `InSlice` cases as false.

### S1-15 / T14 first half - run tooling and `verify-slice`

`make summarize-run` = `go build -o bin/indexer ./cmd/indexer` + `caffeinate -i bin/indexer summarize --level function --concurrency 1`; `make summarize-pause` = `pkill -INT -f "bin/indexer summarize"` (finishes the in-flight item); `make summarize-status` = `indexer summarize --status`; `make verify-slice` = `indexer walk --verify-slice`.

`walk --verify-slice` recomputes the sha256 of the sorted in-slice path list from the corpus and the CURRENT slice definition (`walk.Walk` + `InSlice`), then compares it with the embedded `internal/walk/slice.sha256` (D-070) — recomputing from the slice definition, not the database, is what makes a drop-list edit fail the check:

```
$ make verify-slice            -> in-slice paths 876; expected == computed 2738a003...; match; exit 0
$ (temp: add pkg/scheduler to the drop list)
$ make verify-slice            -> computed 4695e383...; slice hash drift; exit non-zero
$ (reverted)
$ make verify-slice            -> match; exit 0
```

`summarize --status` counts only the current prompt version (`fn-v1`): a `file`-level row and an `fn-v0` row are not counted (test). ETA is sampled over a wall-clock window (`--eta-window`, default 30s) because the frozen `summaries` table has no completion timestamp; it returns immediately when nothing is pending (`pending 0 done 28 failed 0`, `eta 0s`). Pause/resume is verified by counts on a small fake-Ollama run: cancel at request 6, exactly 6 `done`, no partial rows (no `done` with NULL text, no `failed` with text), resume completes, 0 duplicates. The keep-awake assertion is observable via `pmset -g assertions | grep -i caffeinate` while `caffeinate -i` runs; the owner starts the full run. `make test` 0 SKIP; `make lint` 0 issues.

### S1-16 / T14 second half - quality gate (PREPARED, awaiting the author)

The gate sample was summarized: `bin/indexer summarize --level function --sample 200 --seed 3` -> `eligible 6673, selected 200, done 200, failed 0`; among the 200, `too_large = 0` and `truncated = 0` (all `fn-v1`). A reproducible helper `tools gate` (`go run ./cmd/tools gate`, `cmd/tools/gate.go`; moved out of the indexer by S1C-06, D-071; read-only, no Ollama) draws 30 of the done rows with seed 3 and writes the grading artifacts:
- `.scratch/S1/quality-gate-fn-v1.md` — 30 sections (qualified name, input_hash, code, summary, blank verdict).
- `.scratch/S1/quality-gate-fn-v1.jsonl` — 30 lines `{qualified_name, input_hash, verdict:"", criterion:""}`.

The author grades each summary good/bad against the D-068 rubric (first failing criterion), edits the jsonl, and starts `make summarize-run` in their own terminal. The agent does not grade or start the run. The 200 gate rows stay; the full run skips them by `input_hash`.

### Earlier `summarize --status` ETA bug (fixed by 6971672, superseded by S1C-12)

Observed during the fn-v1 run: `make summarize-status` printed `pending 1`, `remaining 1`, `eta 6s` while about 6,300 functions were still unsummarized.

Cause (verified): `Status` set `Remaining` from the `pending` count. Rows are written `pending` one at a time by `markPending` when an item is picked up, so with `--concurrency 1` the pending count is never above 1. The ETA divided that count by the measured rate, so it was always a few seconds. The 30-second rate (`PerHour`) was correct but noisy, swinging between 600 and 960 fn/h.

Fix (commit 6971672): `Status` counted eligible rows with `r.eligible(ctx)` (the same query `Run` uses) and `statusReport` set `Remaining = max(0, eligible - done - failed)`, subtracting failed unless the status call was a `--retry-failed` run; the CLI passed the existing flag through and `pending` stayed printed as in-flight. Tests: `TestStatusReportETA` (eligible 6673, done 379, 600 fn/h gives remaining 6294 and an ETA of about 10.5 h), `TestStatusReportFailedRemaining` and `TestStatusReportNoProgress` (delta 0 and delta below 0 give no ETA). `bin/indexer` was not rebuilt, so the live run was untouched. The optional longer-window or run-start rate was left as it was.

Superseded: S1C-12 replaced the 30-second sampled rate with a rate from the last 100 timestamped completions of the current prompt version, so the windowed-rate concern above no longer applies.

### S1 close-out sign-off (2026-10-13)

Checked locally only: `make ci-local` (vet, strict test run with 0 `--- SKIP`, `golangci-lint` "0 issues.", `lint-scripts: running shellcheck on scripts/*.sh`, actionlint) passes; nothing is pushed (D-069). `make verify-slice`: slice hash match. `select prompt_version, status, count(*) from summaries where level='function' group by 1,2` returns only `fn-v1|done|6694`; `make summarize-status` shows pending 0, done 6694, failed 0, remaining 0, so the overnight run is complete and no resume or second reading was needed (owner accepted recording this instead).

Deviations from the original S1 spec, all accepted:
- Slice has three roots; client-go is dropped (D-070). Eligible functions 6,673, below the 15,000-25,000 expectation (D-070).
- Corrected mining yield is 25 against the 30 threshold; accepted, question set is finished in S4 (D-072). D-070's 150 was overstated.
- The one-off gate command lives in `cmd/tools`; the workspace fallback in `callgraph-gt --check` and the retry-once on rate limiting are kept and documented (D-071).
- `summarize --status` ETA is derived from the last 100 timestamped completions of the current prompt version (nullable `summaries.completed_at`, migration 00004), prints "ETA unknown" with a reason below two completions, and returns immediately.
- Failed rows are refreshed when their input hash changes; a moved symbol keeps its summary; `make summarize-pause` signals only the real process; `make test-live` is the opt-in live-model target and the default run needs no model server.
- Added `make mine-yield` so the probe reads `GITHUB_TOKEN` and `KUBE_TAG` from `.env`.

The function-summary run is complete; later stages (S2 to S4) continue from these rows.

