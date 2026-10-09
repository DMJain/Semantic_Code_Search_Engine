# P2-S7 - Opt-in heavy mode

## Goal
Full chat and summaries when a bigger model is available: Ollama detection turns chat on automatically; without Ollama the bigger GGUF model (D-036) is downloaded only on request; memory discipline keeps one model loaded at a time.

## Entry criteria
P2-S2 (runtime, manager), P2-S5 (engine, focus), P2-S6 (MCP) done. D-036 names the heavy-mode default model.

## Links
[v2-architecture.md#heavy-mode](../v2-architecture.md#heavy-mode), [#memory-discipline](../v2-architecture.md#memory-discipline), [#inference-runtime](../v2-architecture.md#inference-runtime), [#mcp-tools](../v2-architecture.md#mcp-tools); [architecture.md#qa-loop](../architecture.md#qa-loop), [#stage-summarize](../architecture.md#stage-summarize), [#semantic-retriever](../architecture.md#semantic-retriever), [#ollama-requests](../architecture.md#ollama-requests); decisions D-036, D-038, D-053.

## Tasks

### P2-S7-T01 - Ollama detection and LLM provider selection
- Why: heavy mode turns on automatically when Ollama is present.
- Depends on: none
- Touches: `internal/heavy/detect.go`, `internal/heavy/provider.go`
- Details: `Detect(ctx)` probes `OLLAMA_URL` (default `http://localhost:11434`) `/api/tags` with a 500ms timeout and returns `{Available, Models}`. `Provider(ctx, cfg) (llm.LLM, source string)` selection order: Ollama with the D-036 model if it is pulled; else Ollama with any pulled model matching a configured allow-list (`config.toml` `[heavy] ollama_models`); else none. Never pulls an Ollama model by itself; it prints the `ollama pull <model>` command instead.
- Acceptance: tests with `httptest.Server`: reachable with the model -> Ollama provider; reachable without the model -> `none` plus the pull hint; unreachable (timeout) -> `none` within 600ms.
- Tests: `httptest` cases.
- Out of scope: managed download.
- Owner: agent

### P2-S7-T02 - Managed bigger-model download on request
- Why: users without Ollama can still opt into chat and summaries.
- Depends on: none
- Touches: `internal/models/manifest.go`, `cmd/lodestone/chat.go` (prompt helper)
- Details: add the D-036 model to the manifest (role `chat`, Q4_K_M, revision-pinned URL, sha256, size, license). `heavy.EnsureModel(ctx, confirm func(size int64) bool)` asks `Download <name> (<size>)? [y/N]` before any download (`--yes` skips), then reuses the downloader. No download ever happens without confirmation or `--yes`.
- Acceptance: tests with the `httptest` downloader: declining downloads 0 bytes; accepting verifies the checksum; the manifest test (P2-S2-T01) still passes with the new entry.
- Tests: unit tests.
- Out of scope: running the model.
- Owner: agent

### P2-S7-T03 - Memory discipline in the runtime wrapper
- Why: heavy mode must stay within a bounded footprint ([v2-architecture.md#memory-discipline](../v2-architecture.md#memory-discipline)).
- Depends on: P2-S7-T02
- Touches: `internal/runtime/`
- Details: chat loads use Q4_K_M weights, `num_ctx = min(model max, ceil(prompt_tokens * 1.25) + max_new_tokens)` computed per request from the bytes/4 estimate and rounded up to the next 1024, a quantized KV cache (`q8_0` or `q4_0` per D-053 capability), and `unload()` after the idle timeout. Loading the chat model first unloads the embedder and vice versa (the manager lock). Expose `Stats()` for the chosen `num_ctx` and KV type for logging.
- Acceptance: fake-runtime tests: a 3,000-token prompt yields `num_ctx = 4096`; alternating embed and chat calls never overlap loads; the KV type is passed to the runtime; idle unload fires (fake clock).
- Tests: unit tests with the fake runtime.
- Out of scope: measuring footprints (P2-S8).
- Owner: agent

### P2-S7-T04 - Query rewriting from history and evidence reuse
- Why: chat follow-ups need context without re-retrieving everything.
- Depends on: P2-S7-T01, P2-S5-T04
- Touches: `internal/chat/rewrite.go`, `internal/chat/session.go`
- Details: `prompt_version = "p2-rewrite-v1"`: given the last 3 turns (question, short answer) and the new utterance, produce one standalone search query (temperature 0, `max_tokens` 60). A deterministic rule pre-check skips the LLM when the utterance contains an explicit identifier or the follow-up resolver already resolved it. Evidence from previous turns is kept in the session (symbol and file ranges); new retrieval results are merged and de-duplicated; items already shown are marked `seen`.
- Acceptance: tests with a fake LLM: a follow-up with a backticked identifier makes 0 LLM calls; `"what about the error case?"` after a turn about a function triggers 1 rewrite call and the rewritten query is the one searched; evidence from turn 1 is available in turn 2 without a new search when the rewritten query's results are a subset.
- Tests: fake-LLM unit tests.
- Out of scope: the answer step.
- Owner: agent

### P2-S7-T05 - `chat` command with answers and citation validation
- Why: the heavy-mode user experience.
- Depends on: P2-S7-T03, P2-S7-T04
- Touches: `cmd/lodestone/chat.go`, `internal/chat/answer.go`
- Details: `lodestone chat [--yes]`: REPL (also `lodestone chat "<question>"` one-shot). Each turn: rewrite -> `Engine.Search` -> constrained multi-hop loop reusing Phase 1 `internal/qa` (candidates from graph edges, max 3 hops, token cap) through the Store interface -> answer with the same citation format as Phase 1 -> citation validation against the evidence (invalid removed). The provider comes from P2-S7-T01, else the managed download flow (P2-S7-T02) after confirmation, else the command prints how to enable heavy mode and exits 2. Prompts keep their `prompt_version` strings; the LLM client is the Ollama or runtime-backed `llm.LLM` (no Groq).
- Acceptance: tests with a fake LLM and fixture index: a scripted 2-turn session returns answers whose citations all fall in the evidence; an injected invalid citation is removed; with no provider and no `--yes` the command exits 2 with the enable message and downloads nothing.
- Tests: scripted fake-LLM tests.
- Out of scope: MCP `ask`.
- Owner: agent

### P2-S7-T06 - MCP `ask` tool, registered only when an LLM is available
- Why: agents get Q&A only where it works.
- Depends on: P2-S7-T05, P2-S6-T07
- Touches: `internal/mcpserver/tools_ask.go`
- Details: at `tools/list` time call `heavy.Provider`; register `ask{question: string}` only if a provider exists, otherwise it is absent (not a stub). The tool returns the answer text, validated citations and the evidence list.
- Acceptance: golden `tools/list` transcript with no provider lacks `ask`; with a fake provider includes it; an `ask` call returns citations inside evidence.
- Tests: transcript tests with a fake provider.
- Out of scope: streaming.
- Owner: agent

### P2-S7-T07 - `index --summaries` and summary-aware retrieval
- Why: opt-in summaries improve semantic search like Phase 1's A2.
- Depends on: P2-S7-T03
- Touches: `internal/indexer/summaries.go`, `internal/retrieve/coarse.go` (Store-based)
- Details: `lodestone index --summaries [--yes]` runs after the normal phases: function summaries for functions with body of at least 3 lines (prompt `fn-v1`), then file and package levels (`file-v1`, `pkg-v1`) with the same input hashing, `num_ctx` check and truncation failure as Phase 1, using the D-036 model through the manager (embedder unloaded first), then embeds the summaries (embedder reloaded after the chat model unloads). The coarse-to-fine retriever from Phase 1 runs on the SQLite store when summary rows exist, with the weights recorded in D-020. Resumable by `input_hash`.
- Acceptance: on the fixture repo with a fake LLM: summaries are created once and a rerun makes 0 LLM calls; load order from the fake runtime is embedder, chat, embedder with no overlap; retrieval on the fixture uses the coarse retriever only when summaries exist (spy).
- Tests: fake-runtime and fake-LLM tests.
- Out of scope: evaluating quality (S9 already did on Phase 1).
- Owner: agent

## Measurements to record
- Heavy-mode peak physical footprint during chat and during `--summaries` on a small repo (early reading in Results; formal runs in P2-S8).

## Exit criteria
Chat works with Ollama and with the managed model on request; no download without consent; only one model loaded at a time (proved with the fake); `ask` appears only when available; ROADMAP updated.

## Risks + mitigations
- The bigger model is slow on 8GB machines: documented as opt-in; footprint numbers published.
- Prompt drift versus Phase 1: prompts keep their versions; only the transport changes.

## Demo check
With Ollama running: `lodestone chat "how does the retry logic work?"` shows an answer with citations. Without Ollama: the same command explains how to opt in.

## Results
(empty)
