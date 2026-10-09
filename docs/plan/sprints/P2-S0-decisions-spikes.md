# P2-S0 - Decisions & spikes

## Goal
Settle the four open technology choices with small throwaway spikes, and apply the model/dimension selection rule to Phase 1 DEV results. Each task ends in a `decisions.md` entry. No production code.

## Entry criteria
D-046 records the author's explicit approval of Phase 1. Phase 1 TEST results exist. Phase 1 DEV results for E1 (A3n), E2 and M1 exist (S8).

## Links
[v2-architecture.md#store-interface](../v2-architecture.md#store-interface), [#sqlite-schema](../v2-architecture.md#sqlite-schema), [#compact-vectors](../v2-architecture.md#compact-vectors), [#inference-runtime](../v2-architecture.md#inference-runtime), [#model-manager](../v2-architecture.md#model-manager), [#mcp-tools](../v2-architecture.md#mcp-tools), [#distribution](../v2-architecture.md#distribution); decisions D-041, D-048, D-052 to D-056.

## Spike rules
Spike code lives in `spikes/<name>/` (its own `go.mod`, excluded from CI builds and from `go build ./...`); it is kept for reproducibility and never imported by production code. Each spike lists the candidates, the measured numbers and the choice in its decision entry.

## Tasks

### P2-S0-T01 - Verify the selection-rule inputs and record the model and dimension choice
- Why: Phase 2's embedding model and dimension come from Phase 1 DEV evidence only (D-041, D-056).
- Depends on: none
- Touches: `docs/plan/decisions.md` (D-056)
- Details: confirm `eval/results/A3n/dev.json`, `eval/results/E2/dev.json`, `eval/results/M1-qwen3-256/dev.json`, `eval/results/M1-nomic-256/dev.json` exist. Apply D-041 step by step: (1) model: higher DEV nDCG@10 under A3n between E1 and E2; if the paired-bootstrap CI of the difference includes 0, choose the lower-memory model (smaller GGUF footprint). (2) dimension for the chosen model: 256 unless its M1 DEV result shows an nDCG@10 loss whose CI excludes 0. (3) int8 is NOT decided here (P2-S8). If E2 or M1 results are missing (dropped per the Phase 1 priority list), apply the fallback in D-041 and say so. Print the numbers and the CIs into D-056 and state that Phase 1 TEST numbers were not used.
- Acceptance: D-056 lists model, dimension, the DEV nDCG@10 of both candidates with the CI of the difference, which branch of the rule applied, and the sentence `Phase 1 TEST results were not used`; the rule function rejects any path ending in `test.json` (unit test), and the command used takes only `dev.json` paths.
- Tests: unit test (in `cmd/eval`) of the rule function on four synthetic cases covering each branch, plus the `test.json` rejection case.
- Out of scope: int8, GGUF sourcing (P2-S0-T06).
- Owner: agent

### P2-S0-T02 - Spike: SQLite driver and vector approach
- Why: storage drives binary size, cgo needs and search speed (D-052).
- Depends on: none
- Touches: `spikes/sqlite/`, `docs/plan/decisions.md` (D-052)
- Details: compare a pure-Go driver (`modernc.org/sqlite`) with a cgo driver (`mattn/go-sqlite3`) on: FTS5 availability, recursive CTE support, build time, binary size, cross-platform build on the three target platforms (macOS natively; linux/arm64 and linux/amd64 in `golang` Docker images, amd64 under emulation; record build success and build time per platform), and read/write speed on 1M rows. Vectors: store int8 256d BLOBs and measure brute-force top-10 over 100k and 1M vectors (time and physical footprint) with plain Go loops; separately try the `sqlite-vec` extension if loadable by the chosen driver. Decide: driver, and whether a vector extension is needed at all.
- Acceptance: D-052 contains a table of the measurements per candidate and the choice; `cd spikes/sqlite && go test -bench . ./...` runs and prints the same table columns as D-052 (values may vary by up to 20%); FTS5 query with identifier-split tokens returns the expected row in the spike test.
- Tests: spike benchmarks only.
- Out of scope: production schema.
- Owner: agent

### P2-S0-T03 - Spike: locate and pin GGUF sources for both embedding models
- Why: the runtime spike (T04) needs real GGUF files, and the model manager needs fixed, verifiable sources (D-055).
- Depends on: none
- Touches: `spikes/gguf/`, `docs/plan/decisions.md` (D-055)
- Details: for `qwen3-embedding-0.6b` and `nomic-embed-text` find GGUF files (official repositories first) at Q8_0 and one 4-bit quant; record revision-pinned URL (commit hash, not `main`), size, sha256, license and whether redistribution links are allowed; download them into the spike cache and verify the sha256. Probe results are T06.
- Acceptance: D-055 lists per model and quant: URL with revision, size, sha256 (verified by `shasum -a 256 -c` over the downloaded files), license; the downloaded files exist for T04.
- Tests: spike only.
- Out of scope: the runtime, probes, downloading inside the product.
- Owner: agent

### P2-S0-T04 - Spike: inference runtime (in-process cgo vs managed subprocess)
- Why: decides memory behavior, unloading and distribution complexity (D-053).
- Depends on: P2-S0-T03
- Touches: `spikes/runtime/`, `docs/plan/decisions.md` (D-053)
- Details: compare (a) in-process Go bindings to llama.cpp (cgo) with (b) a managed `llama-server`-style subprocess started on demand. Measure with the Q8 and Q4 GGUF files downloaded in T03: embedding throughput on 1,000 chunks, load time, physical footprint loaded and after unload (does memory return to baseline?), crash isolation, Metal acceleration on macOS, and what each needs to ship on macOS arm64 and Linux amd64/arm64 (how the runtime binary or library is obtained and verified). Record the choice and the idle-unload mechanism.
- Acceptance: D-053 has the comparison table, the chosen approach and the unload mechanism; the spike proves footprint after unload is within 20MB of the pre-load baseline for the chosen approach (number in D-053).
- Tests: spike benchmarks only.
- Out of scope: model manager.
- Owner: agent

### P2-S0-T05 - Spike: Go MCP SDK
- Why: the MCP server must work with Claude Code and Cursor (D-054).
- Depends on: none
- Touches: `spikes/mcp/`, `docs/plan/decisions.md` (D-054)
- Details: build the same one-tool stdio server with the official Go MCP SDK and one community SDK (`mark3labs/mcp-go`). Compare: protocol version support, tool schema generation, stdio robustness (no stray stdout writes), maintenance status, dependency weight. Verify the winner against a real client with `claude mcp add` pointing at the spike binary and one tool call.
- Acceptance: D-054 contains the table, the SDK version pinned and the client verification result (client version, tool call output).
- Tests: spike only.
- Out of scope: real tools.
- Owner: agent

### P2-S0-T06 - Spike: probe the pinned GGUF files against Ollama's vectors
- Why: the model manager needs fixed, verifiable sources (D-055).
- Depends on: P2-S0-T03, P2-S0-T04
- Touches: `spikes/gguf/`, `internal/models/testdata/reference.json`, `docs/plan/decisions.md` (D-055)
- Details: for the files pinned in T03: run each through the runtime from the P2-S0-T04 candidate: pass the Phase 1 probe, and compare vectors against Ollama's for 20 fixed strings (cosine similarity per string; record min and mean). Verify the prefix templates from the Phase 1 registry still apply. Write `internal/models/testdata/reference.json` (the 5 fixed strings and their Ollama vectors, used by P2-S2-T04) and record the cosine threshold (minimum acceptable cosine, expected at least 0.99) as an explicit field of D-055.
- Acceptance: D-055 lists per model and quant: probe PASS/FAIL, min and mean cosine versus Ollama and the threshold field; `internal/models/testdata/reference.json` exists and parses; a model whose probe fails is marked unusable.
- Tests: spike only.
- Out of scope: downloading inside the product.
- Owner: agent

## Measurements to record
All spike tables in the corresponding decisions (D-052 to D-056); nothing in eval results.

## Exit criteria
D-052, D-053, D-054, D-055, D-056 are filled with measured data (D-055 by T03 and T06); ROADMAP updated.

## Risks + mitigations
- Pure-Go SQLite lacks a needed feature: the cgo driver is the fallback and D-048 already requires native runners.
- No GGUF passes the probe for a model: that model is unusable for Phase 2 and the other model is chosen regardless of D-056, recorded as an exception.
- In-process runtime cannot return memory: subprocess wins by rule.

## Demo check
Open decisions D-052 to D-056 and read the tables; run `cd spikes/sqlite && go test -bench .`.

## Results
(empty)
