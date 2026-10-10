# Open questions

Items resolved by a task are recorded in the named decision and deleted here.

- **Q-01**: Pinned tag (D-006), final slice size and mining yield (D-005): S1.
- **Q-02**: Groq model ID and free-tier RPM/TPM limits (D-007): read from Groq docs at S6-T01/T02.
- **Q-03**: Per-model card facts for `qwen3-embedding:0.6b` and `nomic-embed-text` (prefixes, MRL, context) (D-003): S0-T05/T07.
- **Q-04**: Whether VTA over the slice plus dependencies fits in 24GB memory; fallback in S2-T07.
- **Q-05**: Whether `staging/src/k8s.io/client-go` can be loaded by `go/packages` from the clone's `vendor/` layout: first tried in S1-T08 (`callgraph-gt --check`, D-067; a load failure stops for the owner before the T13 freeze), then offline loadability in S2-T06.
- **Q-06**: SQLite driver, inference runtime, MCP SDK, GGUF sources (D-052 to D-055): P2-S0.
- **Q-07**: GitHub and `go install` name collisions for `lodestone` (D-034): checked before the first release, P2-S9-T09.
- **Q-08**: The three smoke repos and their pinned shas (D-043): P2-S8-T07.
- **Q-09**: Whether the 3B or 7B model becomes the heavy-mode default (D-036): S9-T08.
- **Q-10**: Agent-efficiency experiment feasibility under the subscription limits (D-042): P2-S8-T10; partial results are reported as partial.
