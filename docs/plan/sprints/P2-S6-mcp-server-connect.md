# P2-S6 - MCP server + `connect`

## Goal
Expose search, symbol lookup and call-graph tools to coding agents over MCP (stdio), and let users register the server in Claude Code and Cursor with explicit confirmation.

## Entry criteria
P2-S5 done (engine, evidence format, focus stack). D-054 picks the SDK.

## Links
[v2-architecture.md#mcp-tools](../v2-architecture.md#mcp-tools), [#connect](../v2-architecture.md#connect), [#focus-stack](../v2-architecture.md#focus-stack), [#resource-targets](../v2-architecture.md#resource-targets); decisions D-038, D-054.

## Tasks

### P2-S6-T01 - MCP server skeleton and stdio hygiene
- Why: a stray byte on stdout breaks the protocol.
- Depends on: none
- Touches: `cmd/lodestone/mcp.go`, `internal/mcpserver/server.go`
- Details: `lodestone mcp [--project <dir>]` starts the server per D-054 over stdio; the project defaults to the cwd's repo root. All logging goes to stderr (and optionally `.lodestone/mcp.log`). The server starts without opening the runtime and exits 0 on stdin close. A missing index returns tool errors that say `run: lodestone index` instead of crashing.
- Acceptance: a test client over pipes completes `initialize` and `tools/list`; a test asserts nothing but protocol frames is written to stdout (capture and parse every line as JSON-RPC); starting in a directory without an index still answers `tools/list`.
- Tests: pipe-based protocol tests.
- Out of scope: tool bodies.
- Owner: agent

### P2-S6-T02 - Tools: `search_code` and `find_symbol`
- Why: the two entry points for agents.
- Depends on: P2-S6-T01
- Touches: `internal/mcpserver/tools_search.go`
- Details: `search_code{query: string, limit?: int(<=20), scope?: string}` returns the engine's evidence JSON (same schema as the CLI) as structured content plus a compact text rendering; `find_symbol{name: string}` returns candidates with kind, path, line range and signature. Tool descriptions state what they return and when to use each, in under 60 words each. Errors return `isError` with an actionable message.
- Acceptance: tests over the pipe client against the fixture index: `search_code` returns the expected top file for 3 queries; `find_symbol` with an ambiguous name returns all candidates; `limit: 50` is rejected with a schema error; response sizes stay under `MaxResponseBytes`.
- Tests: protocol tests with golden JSON.
- Out of scope: call-graph tools.
- Owner: agent

### P2-S6-T03 - Tools: `callers` and `callees`
- Why: structural questions for agents.
- Depends on: P2-S6-T02
- Touches: `internal/mcpserver/tools_graph.go`
- Details: `callers{symbol: string, depth?: 1..3}` and `callees{...}` resolve the symbol (qualified name or unique short name; ambiguity returns the candidate list with `ambiguous_symbol: true`) and return call sites with path, line, hop, and `resolution` including a flag for ambiguous edges.
- Acceptance: tests on the fixture index: callers of a known function equal the expected files; depth 2 adds hop-2 entries; an unknown symbol returns `isError` with `no symbol named X`.
- Tests: protocol tests with golden JSON.
- Out of scope: `ask`.
- Owner: agent

### P2-S6-T04 - Session focus across tool calls
- Why: follow-ups should work for agents too without extra arguments.
- Depends on: P2-S6-T02
- Touches: `internal/mcpserver/session.go`
- Details: one MCP connection maps to one focus-stack session id. Tool calls push focus entries as in the CLI. An optional string parameter `followup` is NOT added; instead `callers` and `callees` accept `symbol: "@focus"` to mean the top of the stack, and `search_code` accepts `query` phrases handled by the follow-up resolver only when the argument `resolve_followups: true` is passed (default false, so agents get predictable behavior).
- Acceptance: test: `find_symbol` then `callers{symbol:"@focus"}` returns callers of the found symbol; with `resolve_followups` unset, the query `"the second one"` is treated as a new search.
- Tests: protocol tests.
- Out of scope: persistence across connections beyond the CLI session store.
- Owner: agent

### P2-S6-T05 - Memory behavior of the server
- Why: idle footprint and lazy model load are product requirements ([v2-architecture.md#resource-targets](../v2-architecture.md#resource-targets)).
- Depends on: P2-S6-T02
- Touches: `internal/mcpserver/`, `scripts/mcp-idle-check.sh`
- Details: ensure the server never builds the runtime until a semantic query runs; after the idle timeout the manager unloads. `scripts/mcp-idle-check.sh` starts `lodestone mcp`, waits 10 seconds after `initialize`, and prints the physical footprint using `scripts/measure-mem.sh`.
- Acceptance: a test with the runtime spy shows 0 runtime constructions after `initialize` and after `find_symbol` and `callers` calls; the script prints a footprint line and exits 0 (the 50MB target is evaluated in P2-S8, here the number is only recorded in Results).
- Tests: spy-based test; shellcheck for the script.
- Out of scope: the formal measurement.
- Owner: agent

### P2-S6-T06 - `connect` and `disconnect`
- Why: one-step setup with consent.
- Depends on: P2-S6-T01
- Touches: `cmd/lodestone/connect.go`, `internal/connect/`
- Details: detect Claude Code (user config via `claude mcp` CLI if available, else `~/.claude.json` project entry) and Cursor (`~/.cursor/mcp.json` or the project `.cursor/mcp.json`). For each target print the exact file, the exact JSON/command change (a unified diff) and ask `Apply? [y/N]`; `--yes` accepts, `--dry-run` only prints, `--target claude|cursor`. Backup the file to `<file>.lodestone.bak` before writing, merge without touching other servers, and be idempotent (running twice changes nothing). The entry runs `lodestone mcp --project <abs path>` using the absolute path of the running binary. `disconnect` removes only the lodestone entry.
- Acceptance: tests on temp home directories: a config with other servers keeps them; second `connect --yes` reports `already configured`; `--dry-run` writes nothing (file mtime unchanged); `disconnect` removes the entry and leaves others; answering `n` writes nothing.
- Tests: temp-home unit tests with golden diffs.
- Out of scope: other editors.
- Owner: agent

### P2-S6-T07 - End-to-end MCP transcripts
- Why: catch regressions in the wire format.
- Depends on: P2-S6-T03, P2-S6-T04
- Touches: `internal/mcpserver/e2e_test.go`, `internal/mcpserver/testdata/transcripts/`
- Details: record request/response transcripts (initialize, tools/list, one call per tool, an error call) over the real binary built in the test (`go build` into a temp dir) against a fixture index; compare to golden transcripts ignoring ids and timestamps.
- Acceptance: `go test -count=1 ./internal/mcpserver/...` passes; modifying a tool description makes the golden test fail.
- Tests: the transcripts.
- Out of scope: real clients.
- Owner: agent

### P2-S6-T08 - [human] Verify in real clients
- Why: protocol tests do not prove a client accepts the server.
- Depends on: P2-S6-T06, P2-S6-T07
- Touches: this file (Results)
- Details: run `lodestone connect` for Claude Code and for Cursor on your machine, restart the clients, and in each ask the agent to find where a known symbol is defined and who calls it in an indexed repo. Record client versions, whether the tools appeared, and the outputs.
- Acceptance: Results lists, for each client: version, tools visible (yes/no), a call that returned evidence (yes/no); any failure is turned into a ticket in this sprint.
- Tests: none.
- Out of scope: efficiency measurement (P2-S8).
- Owner: human

## Measurements to record
- Idle footprint of `lodestone mcp` after `initialize` (value from `scripts/mcp-idle-check.sh`), recorded in Results as an early reading; the formal measurement is in P2-S8.

## Exit criteria
Four tools work over stdio with golden transcripts; `connect` is consent-based and idempotent; both real clients load the server; ROADMAP updated.

## Risks + mitigations
- Client config formats change: `connect` shows the exact diff and `--dry-run`; the detection code is isolated and covered by fixtures of current formats.
- Large outputs overwhelm agents: response caps and `truncated` flags.

## Demo check
`lodestone connect`, restart Claude Code, ask it "who calls <symbol>" in an indexed repo and see the tool call.

## Results
(empty)
