# P2-S4 - Indexer for any repo

## Goal
`lodestone index` works in any repository: phased pipeline (one model loaded at a time), incremental by content hash, low priority in the background style, progress output, `.lodestone/` layout, and the `.gitignore` offer.

## Entry criteria
P2-S2 (runtime, `doctor`) and P2-S3 (scan, languages, chunking) done.

## Links
[v2-architecture.md#repo-layout-of-the-tool](../v2-architecture.md#repo-layout-of-the-tool), [#indexing](../v2-architecture.md#indexing), [#repo-filters](../v2-architecture.md#repo-filters), [#compact-vectors](../v2-architecture.md#compact-vectors), [#memory-discipline](../v2-architecture.md#memory-discipline); [architecture.md#resumability](../architecture.md#resumability); decisions D-049, D-056.

## Tasks

### P2-S4-T01 - Repo root, `.lodestone/` layout, config and lock
- Why: every command needs the same project context.
- Depends on: none
- Touches: `internal/project/`, `cmd/lodestone/index.go` (skeleton)
- Details: `project.Find(cwd)`: walk up to the nearest directory containing `.git`, else the current directory. Layout: `.lodestone/index.db`, `.lodestone/config.toml` (ignore globs, optional `[eval] slice`), `.lodestone/meta.json` (`schema_version`, `model`, `dim`, `quant`, `vector_format`), `.lodestone/lock` (advisory file lock; a second `index` prints who holds it and exits 1). `lodestone index` skeleton opens the project and the SQLite store.
- Acceptance: tests with temp dirs: nested directory finds the git root; non-git directory uses cwd; two concurrent `index` runs: the second exits non-zero with the lock message; `meta.json` mismatch (different model or dim) makes `index` refuse with a message to run `lodestone index --rebuild`.
- Tests: temp-dir unit tests.
- Out of scope: the pipeline.
- Owner: agent

### P2-S4-T02 - Phased pipeline and phase ordering
- Why: only one model may be loaded at a time ([v2-architecture.md#inference-runtime](../v2-architecture.md#inference-runtime)).
- Depends on: P2-S4-T01
- Touches: `internal/indexer/pipeline.go`
- Details: phases `scan -> parse -> graph -> chunk -> embed`; the first four never touch the runtime; the embed phase loads the embedder once, embeds all pending chunks in batches (batch size 32, backpressure by bounded channel), then the manager unloads after the idle timeout (or immediately with `--unload`). Each phase writes status in `meta` so a crash resumes at the earliest incomplete phase.
- Acceptance: test with the fake runtime: during scan/parse/graph/chunk the fake records 0 loads; during embed exactly one load; after `index` completes the fake shows an unload within the idle timeout (fake clock); killing after the chunk phase and rerunning skips scan to chunk.
- Tests: pipeline unit tests with fakes.
- Out of scope: incremental logic, CLI progress.
- Owner: agent

### P2-S4-T03 - Incremental indexing by content hashes
- Why: re-indexing a changed repo must be fast.
- Depends on: P2-S4-T02
- Touches: `internal/indexer/incremental.go`
- Details: scan computes sha256 per file; unchanged files skip parse/graph/chunk; changed files replace their symbols/chunks/edges in one transaction; chunk `content_hash` (header+content) decides re-embedding, reusing stored vectors for unchanged chunks; deleted files and now-ignored files are removed with their rows. Edges from unchanged files to symbols in changed files are re-resolved by re-running the graph phase for files whose callees' file hash changed.
- Acceptance: tests on the fixture repo: second run reports `0 files changed, 0 chunks embedded`; editing one function re-embeds exactly its chunk(s) (fake embedder call count equals the changed chunk count); deleting a file removes its rows; renaming a function updates edges from a caller file that was itself unchanged.
- Tests: fixture-based unit tests with the fake embedder.
- Out of scope: file watching.
- Owner: agent

### P2-S4-T04 - Background-style execution: priority and threads
- Why: indexing must not make the machine unusable.
- Depends on: P2-S4-T02
- Touches: `internal/indexer/priority_unix.go`, `internal/indexer/threads.go`
- Details: on macOS and Linux lower the process priority (`setpriority(PRIO_PROCESS, 0, 10)`; on macOS also set `QOS_CLASS_BACKGROUND` via the supported mechanism if available, else priority only); `--threads N` (default `max(1, NumCPU/2)`) limits `GOMAXPROCS` and the runtime's thread count; `--foreground` skips the priority change.
- Acceptance: tests assert the chosen priority via `syscall.Getpriority` equals 10 while indexing (and 0 with `--foreground`); `--threads 2` results in the runtime fake receiving `threads=2`.
- Tests: unit tests (build-tagged per OS where required, running on both native runners).
- Out of scope: Windows.
- Owner: agent

### P2-S4-T05 - Progress output and cancel/resume
- Why: users must see progress and interrupt safely.
- Depends on: P2-S4-T02
- Touches: `internal/indexer/progress.go`
- Details: stderr only. TTY: a single updating line `phase | done/total | files/s | ETA`. Non-TTY: one log line per 5 seconds. `--json-progress` writes newline-delimited JSON events `{phase, done, total, elapsed_ms}`. SIGINT/SIGTERM: finish the current batch, commit, print `interrupted; run again to resume`, exit code 130. Final summary: files indexed per language, skipped per reason, chunks, vectors, errors, wall time.
- Acceptance: tests: non-TTY output has the expected line shape; JSON events parse; interrupting the pipeline (test sends the cancel context mid-embed) leaves a consistent DB and a rerun completes with the same final counts as an uninterrupted run.
- Tests: unit tests with injected writers and contexts.
- Out of scope: GUI.
- Owner: agent

### P2-S4-T06 - `.gitignore` offer
- Why: the index must not get committed by accident.
- Depends on: P2-S4-T01
- Touches: `internal/project/gitignore.go`
- Details: after the first successful `index`, if `.gitignore` exists (or the repo has `.git`) and does not ignore `.lodestone/`, ask `Add .lodestone/ to .gitignore? [Y/n]` on a TTY; non-TTY or `--no-gitignore` skips; `--yes` accepts. Appending adds a trailing newline-safe `.lodestone/` line. Never modifies anything without the answer.
- Acceptance: tests for each path (accept, decline, already ignored, no `.gitignore` file in a git repo creates one only after acceptance, non-TTY skip); the file content after acceptance ends with exactly one `.lodestone/` line.
- Tests: temp-dir tests with injected stdin.
- Out of scope: other VCS.
- Owner: agent

### P2-S4-T07 - `status` and `index --rebuild`
- Why: users need to see what is indexed and to reset it.
- Depends on: P2-S4-T03
- Touches: `cmd/lodestone/status.go`, `cmd/lodestone/index.go`
- Details: `status` prints index path, size on disk, schema version, model/dim/quant, files and chunks per language, last index time, number of files with errors; `--json` supported. `index --rebuild` deletes `index.db` (after confirmation, `--yes` skips) and indexes from scratch.
- Acceptance: `go run ./cmd/lodestone status` on a fixture index prints all fields; `--json` output matches a golden file and carries `schema_version: 1` (the Go struct lives in `internal/project/status.go`).
- Tests: golden JSON test.
- Out of scope: other commands.
- Owner: agent

### P2-S4-T08 - Error tolerance report
- Why: a bad file must never fail the run.
- Depends on: P2-S4-T03
- Touches: `internal/indexer/report.go`
- Details: collect per-file errors (read, parse panic recovered, chunk, embed truncation) in `index_errors(path, phase, message)`; `index` exits 0 when only per-file errors occurred and prints the count and `lodestone status` hint; exits non-zero only on store/runtime failures. `--strict` makes any file error exit 1.
- Acceptance: running the pipeline over the robustness corpus (P2-S3-T11) exits 0 with the expected error rows; `--strict` exits 1; the rows survive a rerun and clear when the file is fixed.
- Tests: pipeline tests over the corpus.
- Out of scope: auto-fix.
- Owner: agent

### P2-S4-T09 - Parity eval configuration and real-repo smoke
- Why: the Kubernetes parity run needs Phase 1's file set and slice (D-049) and the tool must handle a large repo.
- Depends on: P2-S4-T03
- Touches: `eval/phase2/k8s-parity.toml`, `internal/project/config.go`
- Details: `config.toml` supports `ignore = [...]` globs and `[eval] slice = [prefixes]`, `[eval] exclude_tests = true`; the parity file replicates the Phase 1 exclusions (vendor, `zz_generated*`, `*.pb.go`, `testdata/`, `*_test.go`, generated clients) and the four slice prefixes. `index --config <path>` uses an explicit config file. The slice is applied at query time through the shared collapse helper (D-039), not at index time.
- Acceptance: a test builds the file list for a mini-tree and asserts the parity config excludes exactly the Phase 1 categories; running `lodestone index --config eval/phase2/k8s-parity.toml` on the Kubernetes checkout completes without aborting (the manual run is recorded in Results with file counts that equal Phase 1's walk counts for non-slice-filtered files).
- Tests: config parsing and exclusion unit tests.
- Out of scope: the eval run itself (P2-S8).
- Owner: agent

## Measurements to record
- First-index wall time, incremental no-change wall time, and index size on disk for the fixture repo and for Kubernetes with the parity config (Results; formal numbers in P2-S8).

## Exit criteria
`lodestone index` completes on the fixture repo, the robustness corpus and Kubernetes; incremental rerun does nothing; ROADMAP updated.

## Risks + mitigations
- Embedding Kubernetes with the managed runtime is slow: embedding is the last phase and resumable; the parity run reuses vectors (incremental).
- Priority lowering makes indexing very slow on busy machines: `--foreground` documented.

## Demo check
`cd <any repo> && lodestone index` shows progress, ends with a summary, and a second run reports nothing to do.

## Results
(empty)
