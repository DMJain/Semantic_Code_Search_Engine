# ROADMAP

Details: sprint files in `docs/plan/sprints/`; `architecture.md`, `v2-architecture.md`, `eval-protocol.md`, `decisions.md`. Status: `todo`, `doing`, `done`. Update Status and the sprint's Results after each sprint. Sprints are linear.

## Phase 1: Kubernetes code search + Q&A (benchmark build)
| ID | Goal | Entry | Exit | Status |
|---|---|---|---|---|
| S0 | Verify setup, fix audit findings | skeleton `59a5039` | CI runs the Postgres test; both models probed live; doctor passes and fails correctly; `search_tsv` migrated | doing |
| S1 | Pinned tag, walk, parse, benchmark, mining-yield probe, slice, overnight summary run | S0 | slice FROZEN (D-005); quality-gated function-summary run launched | todo |
| S2 | Call graph (tree-sitter), VTA ground truth, edge P/R by kind | S1 | both graphs; report by call kind | todo |
| S3 | Chunks, headers, `search_tsv`, embeddings (slice), HNSW, A0/A1 CLI | S2 | A0/A1 search; ANN overlap >= 95% | todo |
| S4 | Harness, 50 queries, TEST freeze, B0, A0/A1/B0 on DEV | S3 | `validate --strict`; TEST hash in `eval/queries.test.sha256` | todo |
| S5 | File/package summaries, summary vectors, A2 | S4 | A2 DEV; weights in D-020 | todo |
| S6 | LLM client, lexical/structural, router, RRF, `/search`, traces | S5 | A3, A3n, A4 DEV; router metrics | todo |
| S7 | `/ask`, citations, grading tooling and rubric, trace view | S6 | blind sheet verified; DEV dry run | todo |
| S8 | E2, M1, single TEST run of every config, Q&A grading, report, README, video, GIF | S7 | all TEST results; report; README; video | todo |
| S9 | 3B vs 7B summaries, A2-3B, memory table | S8 | D-036 decided | todo |
| S10 | GATE: Phase 1 handoff | S9 | `phase1-handoff.md`; STOP message given | todo |

## Phase 2: `lodestone`, installable tool for any repo (starts only after D-046)
| ID | Goal | Entry | Exit | Status |
|---|---|---|---|---|
| P2-S0 | Spikes: SQLite, runtime, MCP SDK, GGUF; model/dim choice | D-046 | D-052 to D-056 recorded | todo |
| P2-S1 | Store interface, SQLite store, fixture e2e protecting Phase 1 | P2-S0 | conformance suite on both stores; goldens unchanged | todo |
| P2-S2 | Model manager and runtime, compact vectors | P2-S1 | download, probe, embed, unload | todo |
| P2-S3 | Language registry, filters, tier-1 extractors, fallback chunker | P2-S1 | robustness corpus indexes without errors | todo |
| P2-S4 | `lodestone index`: incremental, background, progress | P2-S2, P2-S3 | indexes fixture and Kubernetes | todo |
| P2-S5 | search/find/callers/callees, focus-stack follow-ups | P2-S4 | CLI goldens; model-free paths proven | todo |
| P2-S6 | MCP server and `connect` | P2-S5 | transcripts; both clients load it | todo |
| P2-S7 | Opt-in heavy mode, `index --summaries` | P2-S6, S9 | chat works; one model loaded at a time | todo |
| P2-S8 | Parity, compactness, resources, smoke, agent efficiency | P2-S7 | Phase 2 report section | todo |
| P2-S9 | CI builds, release, brew, install script, uninstall, README | P2-S8 | first release verified | todo |
| P2-S10 | GATE: Phase 2 handoff | P2-S9 | `phase2-handoff.md`; STOP message given | todo |

## Critical path
Phase 1: S0 -> S1-T01..T14 (benchmark, yield probe, slice freeze, quality gate, summary-run launch) -> overnight function summaries (runs during S2-S4; pause only for S3-T09 embedding) -> S5 -> S6 -> S7 -> S8-T06 (single TEST run) -> S9 -> S10 gate.
Hard gate inside Phase 1: S4-T14 TEST freeze precedes all tuning (S5-T08, S6-T12, S8-T04). Labeling (S4) overlaps the summary run.
Phase 2: P2-S0 -> P2-S1 (needs the fixture e2e first) -> {P2-S2, P2-S3} -> P2-S4 -> P2-S5 -> P2-S6 -> P2-S7 -> P2-S8 -> P2-S9 -> P2-S10 gate.

## Phase 1 priority list (drop in this order; never drop A0-A3, the TEST freeze, or honest reporting)
1. M1. 2. LLM-as-judge. 3. GIF and trace-view polish. 4. A4. 5. E2. 6. Q&A grading from 15 to 10 queries. 7. S2 error analysis beyond the per-kind table.
Dropping M1 or E2 removes the evidence for Phase 2's model and dimension choice (D-041 fallback: qwen3, native dimension). Further cuts need the author's approval.

## Phase 2 drop order (if anything must go)
Agent-efficiency experiment first, then the Homebrew tap in P2-S9 (the install script and `go install` remain), then smoke queries from about 10 to 5 per repo. Never drop the parity runs, the compactness DEV decision, or the Phase 1 protection checks.

## The two gates
See [README.md#gates](README.md#gates).
