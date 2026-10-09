# S7 - Multi-hop Q&A, grading tooling, trace UI

## Goal
Build `/ask` (bounded multi-hop with constrained candidates and validated citations), the single-shot baseline, the blind-grading tooling and rubric, and the server-rendered trace UI used for the demo. Grading of TEST answers happens in S8; S7 validates the tooling on DEV semantic queries.

## Entry criteria
S6 done (A3 pipeline, LLM client, traces, `/search`).

## Links
[architecture.md#qa-loop](../architecture.md#qa-loop), [#trace](../architecture.md#trace), [#trace-ui](../architecture.md#trace-ui), [#llm-client](../architecture.md#llm-client), [#query-service](../architecture.md#query-service); [eval-protocol.md#qa-evaluation](../eval-protocol.md#qa-evaluation), [#reproducibility](../eval-protocol.md#reproducibility). Decisions: D-007, D-018.

## Tasks

### S7-T01 - Evidence set and compression
- Why: state for the loop.
- Depends on: none
- Touches: `internal/qa/evidence.go`
- Details: `Evidence{Items []Item{File, StartLine, EndLine, Symbol, Text, Source}}` with `Add(item) (new bool)` (dedupe by file+range overlap), `Visited` set of qualified names, `Compress(budgetTokens int) string` renders items as `[file:start-end] symbol` + text, dropping lowest-score items then truncating bodies to fit the budget by the [#token-estimate](../architecture.md#token-estimate). `Contains(file, line) bool` for citation checks.
- Acceptance: unit tests: overlapping ranges dedupe; `Compress(500)` output estimate <= 500 and keeps the highest-score item; `Contains` is inclusive on bounds.
- Tests: table tests.
- Out of scope: loop.
- Owner: agent

### S7-T02 - Constrained candidate list
- Why: the hop LLM may only fetch symbols that exist in graph edges of current evidence ([#qa-loop](../architecture.md#qa-loop)).
- Depends on: T01
- Touches: `internal/qa/candidates.go`
- Details: `Candidates(ctx, db, ev Evidence, maxN=15) []Candidate{Symbol, File, Relation: calls|called_by, Resolution}`: from `edges` (`source='treesitter'`) of symbols in the evidence set, both directions, excluding visited, ranked exact > name_only > ambiguous, then by edge count to evidence symbols; ambiguous ones keep the flag and appear last; stable ordering.
- Acceptance: guarded integration test on a fixture graph: visited symbols are absent; candidates never contain a symbol not connected to the evidence; list length <= 15; order is deterministic over 10 runs.
- Tests: guarded integration; unit test for ranking.
- Out of scope: LLM.
- Owner: agent

### S7-T03 - Hop prompt, parsing, stop conditions
- Why: the loop ([#qa-loop](../architecture.md#qa-loop)).
- Depends on: T02, S6-T03
- Touches: `internal/qa/loop.go`, `internal/qa/prompts.go`
- Details: `prompt_version = "qa-hop-v1"`. System: "You answer questions about the Kubernetes codebase using only the evidence. Decide whether you can answer now or must fetch more code." User: question, compressed evidence, numbered candidate list. Response JSON: `{"action":"fetch|answer","symbols":["<candidate numbers or names>"],"reason":"..."}`. Validate: `symbols` must be a subset of the candidates (invalid names dropped; if all dropped treat as no new evidence); one repair retry on invalid JSON. Fetch = load the symbol's chunk(s) into the evidence set and mark visited. Budget: max 3 hops; token cap on the sum of prompt tokens (config `QA_TOKEN_CAP`, default 12000). Stop when action=answer, hop cap reached, a hop adds no new evidence, or the token cap would be exceeded; then the answer step ([T04](#s7-t04---answer-prompt-shared-by-both-systems)) runs. Initial evidence = A3 pipeline top-5 files' best chunks. Each hop appends a `hop` trace event with action, symbols, reason and evidence delta.
- Acceptance: scripted fake-LLM tests: answer at hop 0 -> 0 fetches; fetch then answer -> 1 hop; fetch 4 times -> stops at 3; a fetch of an already-visited symbol -> stop reason `no new evidence`; invalid JSON twice -> goes to answer step with a `error` trace event.
- Tests: scripted fake LLM covering each stop reason.
- Out of scope: answer prompt, citations.
- Owner: agent

### S7-T04 - Answer prompt shared by both systems
- Why: identical constraints make the comparison fair (D-018).
- Depends on: T01
- Touches: `internal/qa/answer.go`
- Details: `prompt_version = "qa-answer-v1"`; one function `Answer(ctx, llm, question, evidence) (text, citations)` used by BOTH `/ask` and the single-shot baseline. Constraints in the system prompt: use only evidence; cite every claim as `[path:line]` with a single line number inside an evidence item; at most 200 words; if the evidence is insufficient say so. Temperature 0, `max_tokens` 400.
- Acceptance: golden test of the rendered prompt; a test asserts that the baseline and `/ask` call the same function (the single `Answer` symbol, checked by a compile-level shared-use test).
- Tests: golden prompt; fake-LLM test.
- Out of scope: citation validation.
- Owner: agent

### S7-T05 - Citation validator
- Why: invalid citations must be removed ([#qa-loop](../architecture.md#qa-loop)).
- Depends on: T01
- Touches: `internal/qa/citations.go`
- Details: regex `\[([\w./\-]+\.go):(\d+)\]` over the answer; each citation valid iff `Evidence.Contains(file, line)`; invalid ones are removed from the text (the bracket only) and listed in `Removed`; output `{Text, Valid[], Removed[]}`; `Precision = len(Valid)/(len(Valid)+len(Removed))`; when no citations were emitted, precision is 0 and a `NoCitations` flag is set so it stays visible.
- Acceptance: table tests: in-range valid; off-by-one out of range removed; unknown file removed; two valid + one invalid -> precision 2/3; no citations -> flag set, precision 0.
- Tests: table tests incl. malformed brackets.
- Out of scope: semantic correctness.
- Owner: agent

### S7-T06 - `/ask` endpoint
- Why: demo and Q&A eval ([#query-service](../architecture.md#query-service)).
- Depends on: T03, T04, T05, S6-T10
- Touches: `cmd/server/ask.go`
- Details: `POST /ask {"question":"...","mode":"multihop"(default)|"single"}` -> `{"trace_id","answer","citations":[{file,line}],"removed_citations":[...],"hops":n,"llm_calls":n,"tokens":n}`. `single` mode = initial evidence (top-10 A3 files' best chunks) + `Answer` only, no hops (this is the baseline of D-018). Stop reason and per-call tokens go in the trace.
- Acceptance: handler tests with fakes: both modes return the shape above; `single` makes exactly 1 LLM call; empty question -> 400; guarded live call returns an answer with >= 1 valid citation for a known DEV query (recorded in Results).
- Tests: handler tests with fake pipeline and LLM.
- Out of scope: UI.
- Owner: agent

### S7-T07 - Trace UI: search page and result view
- Why: the demo UI ([#trace-ui](../architecture.md#trace-ui)).
- Depends on: S6-T10
- Touches: `cmd/server/ui.go`, `cmd/server/templates/*.html` (`embed.FS`)
- Details: `GET /` form (query, mode: search|ask); `POST /ui/search` renders route + reason, per-retriever result tables (file, score, evidence lines; ambiguous edges flagged), fused list; htmx (`hx-post`, vendored `htmx.min.js` in the embed FS, no CDN) swaps the result region. Plain CSS in the template (no build step). Links to `/ui/trace/{id}`.
- Acceptance: `curl -s localhost:8080/ | grep -c "<form"` = 1; handler test renders a fixture search result containing the route name, a result row and an ambiguous flag; page works with JavaScript disabled via a normal form post (documented in Results).
- Tests: `html/template` render tests against golden HTML fragments; handler tests.
- Out of scope: hop view (T08).
- Owner: agent

### S7-T08 - Trace UI: ask and trace pages
- Why: show hops, reasons and citations.
- Depends on: T06, T07
- Touches: `cmd/server/ui.go`, templates
- Details: `/ui/ask` posts to the `/ask` logic; the answer page shows answer text with citations as links to `https://github.com/kubernetes/kubernetes/blob/<tag>/<file>#L<line>` (tag from config), removed citations struck through; `/ui/trace/{id}` renders the saved payload: route and reason, each retriever's results, each hop (action, symbols, reason, evidence added), each LLM call (model, latency, tokens, cache hit, fallback). Unknown trace ID -> 404 page.
- Acceptance: handler tests with a fixture 2-hop trace render both hops with reasons and the LLM call table; 404 test; manual: ask a DEV question in the browser and click through to its trace (recorded in Results).
- Tests: golden HTML fragments; 404 handler test.
- Out of scope: styling polish (S8 GIF task).
- Owner: agent

### S7-T09 - Q&A grading rubric (human) written before any answers
- Why: the rubric must precede the answers it grades ([#qa-evaluation](../eval-protocol.md#qa-evaluation)).
- Depends on: none
- Touches: `eval/qa/rubric.md` (new, committed)
- Details: write the 0/1/2 rubric: 2 = correct and complete with a citation that supports the main claim; 1 = partially correct or correct but missing/weak citations; 0 = wrong, hallucinated, or "cannot answer" when evidence existed. Include 3 worked examples written from KEP/docs knowledge, not from system outputs. Commit before T11 generates any answers (the commit hash is referenced in the Results).
- Acceptance: file exists, committed; its commit predates the first generated answers file (`git log` order checked in T10 Results).
- Tests: none.
- Out of scope: grading.
- Owner: agent

### S7-T10 - Q&A run and blind-sheet tooling
- Why: grading must be blind and reproducible ([#qa-evaluation](../eval-protocol.md#qa-evaluation)).
- Depends on: T06, T09
- Touches: `cmd/eval/qa.go`
- Details: `eval qa-run --split dev|test [--final]` runs both systems on the 15 TEST semantic queries (or DEV semantic queries for `--split dev`) and writes `eval/results/qa/<split>/answers.json` (per query per system: answer, valid/removed citations, hops, LLM calls, tokens, trace id). `eval qa-sheet --seed 77` writes `eval/qa/<split>/sheet.md` containing for each query two anonymous answers labeled `A`/`B` in seeded random order showing ONLY answer text and citations (no hops, traces, tokens, system names) and a `key.json` (mapping, stored apart and gitignored until grading ends). `eval qa-grade --in grades.csv` joins grades (0/1/2 per answer) with the key and writes `eval/results/qa/<split>/summary.json`: mean grade per system, citation precision per system, hops (multihop), tokens and LLM calls per answer per system, grade-vs-cost table. `--split test` requires `--final` and refuses a rerun.
- Acceptance: sheet contains no occurrence of `multihop`, `single`, `hops`, `trace`, or token counts (`grep -ciE` returns 0); same seed gives the same A/B assignment; `qa-grade` on a fixture CSV yields hand-computed means; `--split test` without `--final` exits non-zero.
- Tests: unit tests for shuffling determinism, the leak grep as a test over the rendered sheet, grade aggregation.
- Out of scope: the TEST run (S8).
- Owner: agent

### S7-T11 - DEV dry run of Q&A grading (human)
- Why: validates tooling and rubric on DEV only (TEST is untouched).
- Depends on: T08, T10
- Touches: `eval/results/qa/dev/`
- Details: run `qa-run --split dev` (8 semantic DEV queries, 16 answers), generate the sheet, grade 6-8 answers, run `qa-grade`, and note rubric ambiguities; fix the rubric only by appending clarifications (dated), never rewriting grades. Record that this was DEV and not reported as a result.
- Acceptance: `summary.json` for dev exists; rubric clarifications (if any) committed with a date.
- Tests: none.
- Out of scope: TEST answers.
- Owner: agent

## Measurements to record
- DEV dry-run Q&A summary (tooling check only, not a reported result); latency per `/ask` mode, LLM calls and tokens per answer for both modes ([eval-protocol.md#system-metrics](../eval-protocol.md#system-metrics)).
- Written to this file's Results and `eval/results/qa/dev/summary.json`.

## Exit criteria / Definition of Done
`/ask` and the single-shot mode work; citation validator removes invalid citations; blind sheet verified leak-free; UI shows route, results, hops, citations; rubric committed before answers; ROADMAP updated.

## Risks + mitigations
- Hop LLM returns invalid JSON often: one repair retry plus answer-step fallback; counted in system metrics.
- Groq quota during runs: cache and fallback; fallback answers are flagged and rerun.
- UI time sink: pages are plain templates; polish is a cut-list item.

## Demo check
Open `http://localhost:8080/`, ask "how does the scheduler decide a pod is unschedulable and retry it"; the page shows the route, hops with reasons, an answer with citation links, and a trace page.

## Results
(empty)
