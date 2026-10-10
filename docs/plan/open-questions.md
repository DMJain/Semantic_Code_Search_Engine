# Open questions

Items resolved by a task are recorded in the named decision and deleted here.

- **Q-01**: Pinned tag (D-006), final slice size and mining yield (D-005): S1.
- **Q-02**: Groq model ID and free-tier RPM/TPM limits (D-007): read from Groq docs at S6-T01/T02.
- **Q-03**: Per-model card facts for `qwen3-embedding:0.6b` and `nomic-embed-text` (prefixes, MRL, context) (D-003): S0-T05/T07.
- **Q-04**: Whether VTA over the slice plus dependencies fits in 24GB memory; fallback in S2-T07.
- **Q-05**: Whether `staging/src/k8s.io/client-go` can be loaded by `go/packages` from the clone's `vendor/` layout: first tried in S1-T08 (`callgraph-gt --check`, D-067; a load failure stops for the owner before the T13 freeze), then offline loadability in S2-T06. Evidence (S1-02, 2026-10-10, tag v1.37.0): the clone has `go.work` (`use ./staging/src/k8s.io/...`) and its `vendor/k8s.io` holds only gengo, klog, kube-openapi, system-validators and utils: there is NO `vendor/k8s.io/client-go`, `api` or `apimachinery`, so the `GOWORK=off` with `-mod=vendor` invocation assumed by D-058 cannot resolve client-go; S1-09 and S2-T06 must try the workspace (default `GOWORK`) first. Outcome (S1-09, 2026-10-10): confirmed. The pinned `GOWORK=off -mod=vendor` load fails with `go: inconsistent vendoring ... k8s.io/api: is replaced in go.mod, but not marked as replaced in vendor/modules.txt`; loading under the workspace (`GOWORK=auto`, `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`) succeeds (720 packages, 0 load errors) and `callgraph-gt --check` prints `file-set: equal (1131 files)` (exit 0). `vendor/k8s.io/client-go` is absent at the tag (not a symlink). Offline loadability stays open for S2-T06.
- **Q-06**: SQLite driver, inference runtime, MCP SDK, GGUF sources (D-052 to D-055): P2-S0.
- **Q-07**: GitHub and `go install` name collisions for `lodestone` (D-034): checked before the first release, P2-S9-T09.
- **Q-08**: The three smoke repos and their pinned shas (D-043): P2-S8-T07.
- **Q-09**: Whether the 3B or 7B model becomes the heavy-mode default (D-036): S9-T08.
- **Q-10**: Agent-efficiency experiment feasibility under the subscription limits (D-042): P2-S8-T10; partial results are reported as partial.
