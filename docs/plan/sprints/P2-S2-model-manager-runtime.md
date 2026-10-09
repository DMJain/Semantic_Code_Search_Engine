# P2-S2 - Model manager + runtime

## Goal
Download, verify, cache, load, unload and probe the embedding model through the managed runtime chosen in P2-S0, exposed as an `embed.Embedder`, with MRL truncation and int8 quantization wired in.

## Entry criteria
P2-S0 done (D-053 runtime, D-055 GGUF sources, D-056 model and dimension). P2-S1 done (`internal/vec` quantization exists).

## Links
[v2-architecture.md#inference-runtime](../v2-architecture.md#inference-runtime), [#model-manager](../v2-architecture.md#model-manager), [#compact-vectors](../v2-architecture.md#compact-vectors), [#memory-discipline](../v2-architecture.md#memory-discipline); [architecture.md#stage-embed](../architecture.md#stage-embed) (probe), [#ollama-requests](../architecture.md#ollama-requests); [eval-protocol.md#memory-measurement](../eval-protocol.md#memory-measurement); decisions D-038, D-053, D-055, D-056.

## Tasks

### P2-S2-T01 - Model manifest
- Why: one compiled-in source of truth for what can be downloaded.
- Depends on: none
- Touches: `internal/models/manifest.go`, `internal/models/manifest_test.go`
- Details: `Manifest{Models []Model{Name, Role: embed|chat, File, URL, SizeBytes, SHA256, License, Quant, Dim, MRL bool, Templates}}` filled from D-055/D-056. URLs are revision-pinned (no `main` branch refs). `Lookup(name)` fails for unknown names. Template strings come from the Phase 1 registry (`internal/embed`), not copied.
- Acceptance: unit test: every manifest entry has a 64-hex sha256, an `https` URL containing a commit-hash revision, non-zero size, and a template that exists in the registry; `go test -count=1 ./internal/models/...` passes.
- Tests: table tests over the manifest.
- Out of scope: downloading.
- Owner: agent

### P2-S2-T02 - Downloader with resume and checksum
- Why: first-run download must be safe and resumable.
- Depends on: P2-S2-T01
- Touches: `internal/models/download.go`
- Details: `Ensure(ctx, name, progress func(done,total int64)) (path string, err error)`: cache dir `os.UserCacheDir()/lodestone/models/<sha256>/<file>`; if present and size matches and sha256 verifies, return; else download to `<file>.part` with HTTP Range resume, verify sha256, `fsync`, atomic rename. Checksum mismatch deletes the part file and returns `ErrChecksum` with the expected and actual values. Offline error text names the URL and the cache path. A cross-process lock prevents concurrent downloads of the same file.
- Acceptance: tests with `httptest.Server`: full download verifies; interrupted download resumes from the byte offset (server logs a Range header); corrupt payload yields `ErrChecksum` and leaves no final file; second `Ensure` makes 0 HTTP requests.
- Tests: `httptest` cases above plus lock test with two goroutines.
- Out of scope: runtime loading.
- Owner: agent

### P2-S2-T03 - Runtime wrapper: lazy load, single-model lock, idle unload
- Why: memory discipline ([v2-architecture.md#memory-discipline](../v2-architecture.md#memory-discipline)).
- Depends on: P2-S2-T02
- Touches: `internal/runtime/` (implementation per D-053), `internal/runtime/fake.go`
- Details: `Runtime` interface: `Load(ctx, model) error`, `Unload() error`, `Embed(ctx, texts []string) ([][]float32, error)`, `Complete(ctx, req) (llm.CompletionResponse, error)`, `Loaded() string`. A `Manager` owns one `Runtime`, enforces one loaded model at a time with a mutex (loading a different model first unloads the current one), loads lazily on first call and starts an idle timer (default 120s, `LODESTONE_IDLE_SECONDS`) that unloads. A fake runtime records load/unload order for tests.
- Acceptance: tests with the fake: two models requested alternately never overlap loads; idle timer unloads after the timeout (fake clock); a call after unload reloads; `-race` clean. With the real runtime, loading the embedding model, embedding 10 strings and unloading returns without error (guarded by an env var because it needs the downloaded model).
- Tests: fake-runtime unit tests with a fake clock; guarded real-runtime test.
- Out of scope: chat model use (P2-S7).
- Owner: agent

### P2-S2-T04 - Embedder implementation, probe after download, reference check
- Why: the product embedder must pass the same probe as Phase 1 and match the reference vectors.
- Depends on: P2-S2-T03
- Touches: `internal/runtime/embedder.go`, `internal/models/probe.go`
- Details: `NewEmbedder(manager, modelName)` implements `embed.Embedder` using the Phase 1 registry templates for prefixes and L2 normalization; `Dim()` is the model's native dimension. After `Ensure`, `Probe` runs the Phase 1 probe (reuse `internal/embed` probe code) plus a reference check: embed 5 fixed strings and require cosine similarity >= the threshold recorded in D-055 against reference vectors in `internal/models/testdata/reference.json` (written in P2-S0-T06). Probe failure deletes nothing but marks the model unusable in `meta.json` and returns an actionable error.
- Acceptance: guarded test with the real model: probe PASS and min cosine to reference >= D-055 threshold; unit test with the fake runtime returning NaN makes the probe fail with the NaN message; the Embedder returns unit-norm vectors of length `Dim()`.
- Tests: unit (fake) and guarded (real).
- Out of scope: truncation and quantization (next task).
- Owner: agent

### P2-S2-T05 - MRL truncation and int8 quantization in the embedding path
- Why: compact vectors from the first write ([v2-architecture.md#compact-vectors](../v2-architecture.md#compact-vectors)).
- Depends on: P2-S2-T04
- Touches: `internal/runtime/compact.go`
- Details: `Compact{Dim int, Int8 bool}` wraps an Embedder: embeds at native dim, truncates to `Dim` and re-normalizes (only if the manifest says `MRL: true`; otherwise returns an error at construction), quantizes with `vec.QuantizeInt8` when `Int8`. Output type `CompactVector{Q []int8, Scale float32, F []float32}`. Queries use the same truncation but stay float for scoring against int8 documents.
- Acceptance: unit tests: a model with `MRL: false` and `Dim < native` fails at construction; top-10 overlap between native-dim float search and 256d int8 search on 1,000 synthetic clustered vectors is at least 0.9; the stored scale and quantized bytes round-trip through the SQLite store.
- Tests: unit tests as above.
- Out of scope: choosing int8 (P2-S8).
- Owner: agent

### P2-S2-T06 - Memory measurement scripts
- Why: all memory claims use the agreed method (D-038, [eval-protocol.md#memory-measurement](../eval-protocol.md#memory-measurement)).
- Depends on: none
- Touches: `scripts/measure-mem.sh` (method header only), `scripts/measure-mem-linux.sh`
- Details: `scripts/measure-mem.sh` (created in S9-T02: 1 s samples of the summed physical footprint of a process tree) is reused on macOS; this task adds the output header naming the method (`physical footprint`) if missing and creates `scripts/measure-mem-linux.sh`, which runs the command in a cgroup v2 scope or reads the container's `memory.peak` and prints it with the method name `cgroup memory.peak` in the header.
- Acceptance: `shellcheck scripts/measure-mem*.sh` passes; `scripts/measure-mem.sh out.csv -- sleep 3` on macOS prints a header naming `physical footprint`, at least 2 samples and a peak; the Linux script is exercised in CI in P2-S8-T06.
- Tests: shellcheck plus a manual run recorded in Results.
- Out of scope: the measurement runs (P2-S8).
- Owner: agent

### P2-S2-T07 - `lodestone doctor` for the tool
- Why: first-run diagnosis without Docker or Ollama.
- Depends on: P2-S2-T04
- Touches: `cmd/lodestone/main.go`, `cmd/lodestone/doctor.go`
- Details: new binary with subcommand dispatch (reuse the Phase 1 pattern) and `doctor`: prints cache dir path and free space, model presence/size/checksum status, runtime loadability, probe result, and whether Ollama is detected (informational). Never downloads unless `--download`. Exit 0 when everything required for default mode is OK, non-zero otherwise with the fix message (`run: lodestone doctor --download`).
- Acceptance: with an empty cache, `go run ./cmd/lodestone doctor` exits non-zero and prints the missing model and the fix command; after `doctor --download`, it exits 0 and prints PASS for the probe.
- Tests: unit tests for report building with a fake manifest and fake runtime; guarded test for the download path.
- Out of scope: other subcommands.
- Owner: agent

## Measurements to record
- Download size and time for the chosen model, load time, footprint loaded and after unload, embedding throughput on 1,000 chunks (Results; recorded in `eval/results/phase2/runtime.json` by `lodestone doctor --json`).

## Exit criteria
Model downloads, verifies, probes, embeds and unloads; compaction works; `doctor` reports; fake-runtime tests prove one-model-at-a-time; ROADMAP updated.

## Risks + mitigations
- Download source changes or disappears: revision-pinned URLs plus sha256; the manifest test fails if a field is missing.
- Runtime cannot unload memory: decided in P2-S0-T04; the chosen approach meets the unload bound.

## Demo check
`lodestone doctor --download` shows progress, checksum OK and probe PASS; a second run is instant.

## Results
(empty)
