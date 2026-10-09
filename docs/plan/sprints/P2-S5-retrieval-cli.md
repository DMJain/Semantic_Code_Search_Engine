# P2-S5 - Retrieval + CLI

## Goal
Search, find, callers and callees from the command line over the SQLite index, with human and JSON output and deterministic follow-ups through the focus stack. No generative model.

## Entry criteria
P2-S4 done (an index exists for the fixture repo and Kubernetes with the parity config).

## Links
[v2-architecture.md#retrieval](../v2-architecture.md#retrieval), [#focus-stack](../v2-architecture.md#focus-stack), [#commands](../v2-architecture.md#commands); [architecture.md#result-contract](../architecture.md#result-contract), [#router](../architecture.md#router), [#lexical-retriever](../architecture.md#lexical-retriever), [#structural-retriever](../architecture.md#structural-retriever), [#fusion](../architecture.md#fusion); decisions D-031, D-039.

## Tasks

### P2-S5-T01 - Wire the shared retrievers to the Store and compose the default pipeline
- Why: Phase 2 default mode is the A3n semantics (D-031).
- Depends on: none
- Touches: `internal/engine/engine.go`
- Details: `engine.New(store, embedderProvider, cfg)` builds lexical, structural and flat-header semantic retrievers from `internal/retrieve` (unchanged code, calling the Store), the rules-only router (`ROUTER_LLM=off` behavior) with the A3n threshold recorded in D-020, RRF k=60 and the shared collapse helper. The embedder is requested lazily from the provider only when the semantic retriever runs. `Engine.Search(ctx, SearchRequest{Query, Limit, Scope}) (SearchResponse, error)` returns route, reason, results with evidence, and per-retriever timings.
- Acceptance: tests with a fake embedder provider: a lexical-routed query (`where is `Foo` defined`) never calls the provider (spy count 0); a semantic query calls it once; the fixture e2e config for A3n gives the same ranked files through `Engine.Search` on the SQLite store as the Postgres golden.
- Tests: unit tests with spies; the fixture comparison.
- Out of scope: CLI.
- Owner: agent

### P2-S5-T02 - `search` command and output schema
- Why: the primary user command.
- Depends on: P2-S5-T01
- Touches: `cmd/lodestone/search.go`, `internal/engine/output.go`
- Details: `lodestone search "<query>" [--limit 10] [--json] [--route lexical|structural|semantic] [--scope <path-prefix>]`. Human format: rank, path:lines, score, one-line snippet, `[ambiguous]` flag; footer shows route and reason. `--json` emits `{schema_version:1, query, route, reason, results:[{file, start_line, end_line, score, snippet, resolution?, evidence[]}]}`. Exit 0 with an empty list prints `no results`. Errors (no index) say `run: lodestone index`.
- Acceptance: golden human and JSON outputs for 3 fixture queries (lexical, structural, semantic); running in a directory without `.lodestone/` exits 2 with the message above.
- Tests: golden outputs.
- Out of scope: follow-up phrasing.
- Owner: agent

### P2-S5-T03 - `find`, `callers`, `callees`
- Why: direct structural commands.
- Depends on: P2-S5-T01
- Touches: `cmd/lodestone/symbols.go`
- Details: `find <name>` resolves by exact name, then qualified name suffix, then trigram-like fuzzy, listing candidates with kind, path:line and signature; `callers <symbol> [--depth N]` and `callees` run the graph CTE (default depth 1, max 3) and print callers grouped by hop distance with resolution flags; an ambiguous symbol name prints the candidate list and asks to refine (non-interactive: exits 3 with the list). `--json` for all.
- Acceptance: fixture tests: `callers` of a function with two callers prints both at hop 1; a name matching two symbols exits 3 with both listed; `--depth 2` includes second-hop callers marked hop 2.
- Tests: golden outputs.
- Out of scope: interactive pickers.
- Owner: agent

### P2-S5-T04 - Focus stack
- Why: follow-ups without an LLM ([v2-architecture.md#focus-stack](../v2-architecture.md#focus-stack)).
- Depends on: P2-S5-T01
- Touches: `internal/focus/stack.go`
- Details: `Stack` over the store's `focus` table with a session id (`--session`, default `default`); push events: a command that returns results pushes `ResultList{items}`; selecting or finding a symbol pushes `Symbol`; max depth 20. API: `Top()`, `Nth(list, n)`, `Pop()`, `Page(list) (nextOffset)`. A session expires after 24 hours of inactivity (checked on read).
- Acceptance: unit tests: push/pop order; depth limit drops the oldest; expiry (fake clock) returns an empty stack.
- Tests: unit tests.
- Out of scope: utterance parsing (next task).
- Owner: agent

### P2-S5-T05 - Follow-up resolution rules
- Why: "it", "the second one", "show more", "go back" must work deterministically.
- Depends on: P2-S5-T04, P2-S5-T02, P2-S5-T03
- Touches: `internal/focus/followup.go`, `cmd/lodestone/ask_followup.go`
- Details: `Resolve(utterance, stack) (Action, ok)` with rules in a table: `it|that|this|that one` plus a verb (`callers of it`, `what calls that`) -> the top symbol; `the (first|second|third|Nth) one|#N` -> `Nth` of the last result list; `show more|more|next` -> next page of the last query; `go back|back` -> `Pop` and re-show the previous top; `callers`/`callees` alone -> applies to the top symbol. A utterance that matches no rule is treated as a new query. Used by `lodestone search --session S "<text>"` (all follow-ups go through `search`) and by MCP in P2-S6.
- Acceptance: a table test with at least 30 (utterance, stack state) -> expected action rows including negatives (`it` with an empty stack returns `ok=false` and a message `nothing in focus`); the CLI runs a 3-step scripted session on the fixture index: `search "scheduler queue"`, `search "the second one"`, `search "callers of it"`, and the third prints callers of the second result.
- Tests: table-driven unit tests plus a CLI golden transcript.
- Out of scope: any LLM-based rewriting (P2-S7).
- Owner: agent

### P2-S5-T06 - Evidence and snippet format shared with MCP
- Why: CLI and MCP must return the same evidence.
- Depends on: P2-S5-T02
- Touches: `internal/engine/evidence.go`
- Details: `Evidence{File, StartLine, EndLine, Snippet, Score, Source, Resolution}` embedding or aliasing the Phase 1 `retrieve.Evidence` (no second evidence type) with `Snippet` = up to 12 lines around the best-matching lines, whitespace-trimmed, with `...` markers; `MaxSnippetBytes = 1200`. Output size cap per response: 40 results and `MaxResponseBytes = 24000` with a `truncated: true` field and the count omitted.
- Acceptance: unit tests: snippet never exceeds the byte cap; a response with 100 large results is truncated with the flag and still valid JSON; the CLI `--json` and the engine output are byte-identical for the same query.
- Tests: unit and golden.
- Out of scope: MCP transport.
- Owner: agent

### P2-S5-T07 - Model-free paths never load the model
- Why: lexical and structural queries must stay fast and light.
- Depends on: P2-S5-T01
- Touches: `internal/engine/engine_test.go`, `cmd/lodestone/search_test.go`
- Details: add a CLI-level test with a runtime spy: `find`, `callers`, `callees`, and `search` with a backticked-identifier query never construct the runtime; a pure natural-language query does exactly once.
- Acceptance: `go test -count=1 ./cmd/lodestone/... ./internal/engine/...` passes with the spy assertions.
- Tests: the spy tests.
- Out of scope: measuring memory (P2-S8).
- Owner: agent

## Measurements to record
- Query latency p50/p95 per route on the fixture index and on Kubernetes (parity config), recorded by a `lodestone bench-queries` hidden command or the P2-S8 harness; Results holds the first numbers.

## Exit criteria
`search`, `find`, `callers`, `callees` work with human and JSON output; follow-ups pass the table tests; model-free paths proven; ROADMAP updated.

## Risks + mitigations
- Brute-force vector search too slow on large repos: measured in P2-S8; ANN is added only if the measurement requires it (new task then).
- Follow-up rules too eager: a non-matching utterance is a new query; the negative rows in the table guard it.

## Demo check
`lodestone search "how are pods scheduled"`, then `lodestone search --session x "the second one"` and `"callers of it"`.

## Results
(empty)
