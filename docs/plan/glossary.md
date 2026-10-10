# Glossary

## Phase 1
- **Slice**: subset of the corpus that gets vectors and summaries and is the eval scope; marked by `files.in_slice`; frozen at the end of S1 (D-005).
- **Filtered repo**: the pinned Kubernetes checkout after exclusions ([architecture.md#corpus-and-slice](architecture.md#corpus-and-slice)).
- **Chunk**: unit of raw code that is full-text indexed and (in the slice) embedded; rules in [architecture.md#stage-chunk](architecture.md#stage-chunk).
- **Chunk header**: context prepended to a chunk (package, file, signature, top callees/callers).
- **Embedding variant**: `chunk_raw` (no header) versus `chunk_header`.
- **Route**: query type decided by the router: lexical, structural or semantic.
- **Fan-out**: running all retrievers and fusing when router confidence is low (also config A4).
- **Rules-only router**: the router with the LLM disabled (`ROUTER_LLM=off`); used by A3n and by Phase 2.
- **RRF**: reciprocal rank fusion of file rankings, k=60.
- **Collapse helper**: the single `internal/retrieve` function that filters to the slice, then de-duplicates files, then cuts to top-k (D-039).
- **Hop**: one iteration of the `/ask` loop (fetch more evidence or answer).
- **Evidence set**: code snippets and symbols collected during `/ask`; citations must fall inside it.
- **Edge resolution**: `exact`, `name_only`, `ambiguous` confidence of a tree-sitter call edge.
- **Ground-truth graph (GT)**: VTA call graph from `go/packages` over the slice, `edges.source='gopackages'`; evaluation only.
- **DEV / TEST**: 15 queries used for all tuning / 35 queries frozen by hash, each configuration run once.
- **Primary / secondary label**: relevance grade 2 / 1.
- **Frozen**: TEST file committed with its sha256 in `eval/queries.test.sha256` (D-010).
- **Build context**: `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`, identical in `walk` and `callgraph-gt` (D-058).
- **B0**: BM25 keyword baseline. **A0..A4, A3n, A2-3B, E1, E2, M1**: configurations in [eval-protocol.md#configurations](eval-protocol.md#configurations).
- **ANN overlap@10**: agreement between HNSW and exact search top-10 ([eval-protocol.md#ann-overlap](eval-protocol.md#ann-overlap)).
- **MRL**: Matryoshka representation learning; vectors can be truncated and re-normalized.
- **Prompt version**: string stored with every cached or summarized LLM output; part of the cache key.
- **Overnight run**: the pausable function-level summarization run launched at the end of S1.
- **Candidate slice**: the `.go` file set `walk` selects under the four slice path prefixes after exclusions, before the freeze. It becomes the **Slice** once the drop list is applied and it is frozen with its hash (D-005).
- **Drop list**: the ordered subpath prefixes removed whole from the candidate slice to fit the compute budget; recorded at the freeze.
- **Eligible function**: an in-slice `func` or `method` symbol with a body of at least 3 lines, signature line excluded (D-014). Function literals are not symbols. A **skipped function** is an in-slice func or method below that threshold.
- **Raw calls**: call-expression text as written, per symbol, used only as summary-prompt input (D-019); never read by the S2 graph.
- **Qualified name**: the globally unique symbol name: package path plus `Name` or `Receiver.Name`, with `#n` appended from the second duplicate in a package, ordered by `(path, start_line)`.
- **12-month yield / 18-month extension yield**: counts of merged PRs usable as semantic eval queries, counted after every automatable filter, in the 12 months before the pinned tag and the further 6 months; their sum must be at least 30 at the freeze and is recomputed on the frozen slice. (S4 pool "tiers" are a separate, later notion.)
- **Quality gate**: the author's review of 30 seeded summaries from a 200-row sample before the overnight run; at most 3 bad passes (D-067).
- **ci-local**: `make ci-local`, the local mirror of `ci.yml` used until the owner pushes (D-062).
- **Gate**: a handoff sprint (S10, P2-S10) after which the agent stops and waits for the author's explicit approval.

## Phase 2
- **lodestone**: the Phase 2 tool (D-034); binary `cmd/lodestone`.
- **Default mode**: no generative model; search, find, callers, callees, MCP tools.
- **Heavy mode**: opt-in chat and summaries with a bigger model (Ollama or managed download).
- **Store interface**: the abstraction with Postgres (benchmark) and SQLite (tool) implementations.
- **Focus stack**: per-session list of recent symbols and result lists used for follow-ups without an LLM.
- **Tier 1 language**: Go, TypeScript/JavaScript, Python, Java (symbols + heuristic call graph); other text languages use fallback chunking only.
- **Model manager**: downloads, checksum-verifies, caches and probes GGUF models.
- **Compact vectors**: MRL-truncated, int8-quantized vectors stored as BLOBs.
- **Physical footprint**: macOS memory metric used for all memory claims (D-038).
- **Parity**: Phase 2 default mode versus A3n and A1 on the same queries.
- **Smoke test**: small pinned-repo sanity check for other languages; not a benchmark.
