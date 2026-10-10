# S1 - Corpus & symbols

Status: ready-for-agent

Task-level detail (commands, acceptance, evidence to record) lives in the plan sprint file `docs/plan/sprints/S1-corpus-symbols.md` and in the tickets under `issues/`. This spec is the product-level view. Decisions made while writing it: D-062 to D-067.

## Problem Statement

The Kubernetes semantic code search engine has no corpus yet. There is no pinned Kubernetes tag, no list of indexed files, no symbols, and no function summaries, and every later stage (call graph, chunks, embeddings, the evaluation) stands on them. As the developer, I cannot yet tell which files are in scope, whether a tree-sitter parse of Kubernetes gives correct symbols, how fast the local model summarizes, or whether enough merged pull requests exist inside the scope to build 25 semantic evaluation queries. The scope (the slice) becomes the evaluation scope and can never change after it is frozen, so a wrong guess about size, yield or file sets would invalidate everything built later. The function-summary run is the longest job in the project and sits on the critical path; starting it with bad prompts, orphaned rows or an unsafe pause would waste a large part of the compute budget. Finally, CI cannot be observed without pushing, and the parser needs a C toolchain, so the build setup has a trap: a global `CGO_ENABLED=1` would silently change which files the corpus walk accepts.

## Solution

Pin one Kubernetes tag and build, in order: a walk that decides the filtered file set, a parser that stores functions, methods, structs, interfaces and raw call names with stable ids, and an audit of both on the real tag. Prove, by a command, that the walk and `go/packages` see exactly the same files before the slice is frozen. Measure summarization throughput and probe how many evaluation queries can be mined from the candidate slice, then let the author freeze the slice with a recorded hash. Build a pausable, resumable summarization command, check summary quality on a seeded sample with the author as grader, and hand the author the command that starts the overnight run. The local CI mirror (`make ci-local`) and the CGO separation are set up first so the parser can be added safely. S1 is done when the slice is frozen and the author-started run is verifiably progressing, not when it finishes.

## User Stories

1. As the developer, I want `make ci-local` to run the same commands and environment as `ci.yml` against my local Postgres, so that I get CI-equivalent proof without pushing.
2. As the developer, I want `make ci-local` to fail on any skipped test, so that a missing database configuration cannot pass quietly.
3. As the developer, I want the workflow file linted by a pinned tool that needs no installation, so that a typo in `ci.yml` is caught locally and reproducibly.
4. As the developer, I want the C-toolchain setting scoped to building and testing, so that the parser compiles without changing the corpus rules.
5. As the developer, I want a test that sets the C-toolchain variable in the environment and shows the walk's build context still has cgo off, so that the corpus file set cannot drift with my shell.
6. As the developer, I want one Kubernetes tag chosen by a stated rule and pinned in the decision log with its date, go directive and commit, so that every label and result refers to one source tree.
7. As the developer, I want fetching the repository twice to be a no-op, so that setup is safe to repeat.
8. As the developer, I want database changes for symbols, summaries and the parse marker in a reversible migration, so that rollback stays tested.
9. As the developer, I want a duplicate summary row for the same level, target and prompt version rejected by the database, so that two runs can never double-write.
10. As the developer, I want a walk that applies named exclusion rules and prints a count per rule, so that I can see what each rule removed.
11. As the developer, I want files rejected by the Linux/amd64, no-cgo build context excluded and counted under their own rule, so that the indexed files match what the compiler loads.
12. As the developer, I want a file that imports C excluded even when my shell enables cgo, so that results are identical on any machine.
13. As the developer, I want walking twice to report zero changes, so that the stage is idempotent.
14. As the developer, I want the exclusion rules implemented once and reused by the audit and the mining probe, so that three tools cannot disagree about what is excluded.
15. As the developer, I want functions, methods, structs and interfaces extracted with qualified names that preserve pointer versus value receivers, so that a symbol can be found by its full name.
16. As the developer, I want duplicate names in one package (several `init` functions) disambiguated deterministically across files, so that qualified names are globally unique.
17. As the developer, I want raw call names stored per function, so that summaries can mention what a function calls without waiting for call resolution.
18. As the developer, I want parsing twice to report zero files to parse, and an interrupted parse to finish with the same totals as an uninterrupted one, so that the stage is resumable.
19. As the developer, I want a symbol to keep its id when its file is re-parsed, so that summaries never point at the wrong or a missing symbol.
20. As the developer, I want symbols removed from a file to disappear together with their summaries, so that no orphan summary survives.
21. As the developer, I want a summary to count as fresh only when its input hash recomputed from the current symbol matches, so that a shifted duplicate name cannot keep a stale summary.
22. As the developer, I want a seeded random audit of 30 stored symbols checked against the source lines, so that parse quality is measured on the real tag, not assumed.
23. As the developer, I want a command that proves the walk's file set equals the file set `go/packages` loads for the four slice roots, exiting non-zero with both difference lists otherwise, so that tree-sitter and the type-checked graph describe the same code.
24. As the developer, I want a load failure to stop for me with the exact failure recorded, so that I decide how to proceed before the freeze.
25. As the developer, I want one function-summary prompt with a single current prompt-version constant, so that every stage filters on the same version.
26. As the developer, I want every Ollama request to carry an explicit context size and over-long requests refused before sending or flagged after, so that truncated input never becomes a silent summary.
27. As the developer, I want the summarize command to resume after SIGINT without partial rows or repeated model calls, so that the long run can be paused for any reason.
28. As the developer, I want a seeded, stratified sample option that returns the same ids for the same seed, so that the quality gate and benchmark are reproducible.
29. As the developer, I want measured summarization throughput at concurrency 1 and 2 plus file- and package-level prompt rates, so that the slice size comes from measurement.
30. As the developer, I want the benchmark never to write rows into the summaries table, so that it cannot pollute the real run.
31. As the developer, I want a mining-yield probe over merged GitHub pull requests that splits capped search windows, reads the rate limit from the API, caches every response and reuses the cache, so that I learn whether 25 semantic queries are minable without burning the quota twice.
32. As the developer, I want the probe to fail before any network call when the token is missing, and never to print or log the token, so that the secret stays secret.
33. As the author, I want to freeze the slice only after both the size projection and the yield are known and the file-set check passes, so that the evaluation scope is final and sound.
34. As the author, I want the frozen slice recorded as a new decision entry with the tag, commit, size calculation, eligible-function count, ordered drop list, recomputed yields and a hash of the sorted file list, so that the freeze is auditable and the decision log stays append-only.
35. As the developer, I want `make verify-slice` to recompute that hash and fail on any drift, so that later sprints can confirm the scope never moved.
36. As the author, I want to review 30 seeded summaries against a written rubric before the overnight run, with at most 3 bad allowed, so that a poor prompt is caught before it costs compute.
37. As the author, I want a failed gate to bump the prompt version and redo the gate, and a second failure to stop the agent, so that no one loops on a bad prompt.
38. As the author, I want to start the overnight run myself in my own terminal with a command that keeps the Mac awake, so that I control when the machine is busy.
39. As the developer, I want pause and resume verified by counts (not by waiting), so that the check is the same on any machine.
40. As the developer, I want `summarize-status` to count only the current prompt version, so that old-version rows cannot inflate progress.
41. As the reviewer, I want work on a branch stacked on the S0 branch, with one commit per ticket after the plan-document commit, so that I can review each change and merge myself.
42. As the reviewer, I want the sign-off checklist to list the draft-PR push as an owner decision with the unobserved CI risk stated, so that the deferral is explicit.
43. As a future agent, I want each ticket to name its plan task, its blocking tickets, its acceptance checks and its report format, so that a fresh session can do one ticket and one diff.
44. As the developer, I want database integration tests to use their own throwaway databases and never touch dev data, so that tests can run in parallel and cannot wipe real rows.

## Implementation Decisions

- **Branch and process.** `s1-corpus-symbols`, stacked on `s0-setup-verification` (an S0 follow-up commit with the throwaway-database test helper lands there first). Plan-document commit first (this sprint's edits and decisions D-062 to D-067), then the scratch folder, then one commit per ticket. Agents do not push; a draft PR for CI proof is an owner decision on the sign-off checklist (D-061, D-062). Agents never push to or merge into the main branch.
- **Ticket mapping.** `issues/01` to `issues/15` map one-to-one to plan tasks T01 to T15; T15 is new and blocks T04 only. Each header carries "Plan: S1-Tnn". T13 is for the human. T14's agent part ends at the quality gate.
- **Execution order.** 01, 02, 03, 15, 04, 05, 06, 07, 08, 09, 10, 11, then 12 (needs the token), 13 (author), 14. T12 depends on T01, T03 and the token gate, so it can run alongside the benchmark; T13 depends on T08, T11, T12.
- **CGO.** The C toolchain is enabled only for building and testing (CI steps, `make ci-local`, the Makefile build). The corpus build context is Linux, amd64, cgo off in both the walk and `callgraph-gt`, set explicitly and never inherited (D-062, D-058).
- **Schema.** Migration 00003 adds `symbols.raw_calls`, `files.parsed_sha`, `summaries.reason` and a unique index on `(level, target_id, prompt_version)`; it is reversible and no later migration is added in S1.
- **Corpus.** One shallow clone at the pinned tag with `vendor/` kept on disk but never indexed. The exclusion rules are named predicates in one exported function shared by walk, the audit and the mining probe. The candidate slice is the file set under the four prefixes after exclusions; the slice is that set after the drop list.
- **Symbols.** Functions, methods (receiver kind preserved), structs and interfaces; function literals are not symbols. Upsert on qualified name keeps ids stable; a file's vanished symbols and their function summaries are deleted in the same transaction (D-064).
- **Equality check.** `callgraph-gt --check` compares the walk set with the `go/packages` set under the identical build context; client-go is loaded by import path from the root module; exit codes 0/1/2 (D-066).
- **Summarization.** One prompt version constant; explicit context size in every request; `too_large` and `truncated` failures recorded with a reason; selection is in-slice functions and methods with a body of at least 3 lines. Resume by status and input hash; SIGINT finishes the in-flight item and exits 0.
- **Quality gate.** Stratified seeded sample of 200, seeded 30 graded by the author, at most 3 bad, rubric and file format in D-067. Passing rows are kept; a failed version's rows are deleted at T14.
- **Mining probe.** GitHub REST search in weekly merged-date windows, halving any window that reaches the 1,000-result cap, rate limit read from the API, responses cached by query and window and reused by S4. Token checked with `test -n` only.
- **Freeze.** The author applies the compute-budget rule and the yield rule, then a new decision entry "Slice frozen (refs D-005)" is appended with the fields listed in story 34, and the hash is also stored in a machine-readable file that `make verify-slice` reads. No CI hash pin. A re-freeze needs an owner-authorized superseding entry.
- **Definition of done.** Per D-063: slice frozen with hash, gate passed, owner-started run progressing across two status readings, pause and resume verified by counts, `make ci-local` green with 0 skips.
- **Execution accounting.** Throughput and wall times are reported as metrics only; no wall-clock thresholds appear in acceptance checks.

## Testing Decisions

- **What makes a good test here.** Assert external behavior only: files kept and excluded per rule, rows and columns visible in a real database, bytes sent to a fake Ollama or fake GitHub, exit codes and output of the commands. No assertions on private helpers. Golden files are never rewritten by `make test` or CI.
- **Seam 1 - walk against a module fixture.** A small committed module tree gives a golden kept-file list and per-rule counts, including negatives (`_windows.go`, a `//go:build windows` file) and an `import "C"` file excluded by both walk and `go/packages`. The walk set equals the `go/packages` set under the identical context, run with the C-toolchain variable set in the environment. An integration test covers idempotency.
- **Seam 2 - parse against golden fixtures and real Postgres.** Goldens for pointer and value receivers, generics, grouped type blocks, closures, method chains and raw calls; duplicate `init` functions numbered across files; id stability after a re-parse with the marker cleared; a removed function leaves no symbol and no summary (orphan query returns 0); an inserted extra `init` makes the shifted summaries stale.
- **Seam 3 - summarize against a fake Ollama and real Postgres.** The exact prompt bytes; the context size in every request body; a symbol under 3 body lines is skipped and one at 3 included; `too_large` with zero HTTP calls; `truncated`; a rerun makes zero calls; SIGINT mid-run leaves no partial rows, resumes, and yields zero duplicates; same seed gives byte-identical sample lists; concurrency 2 under the race detector.
- **Seam 4 - mining probe against a fake GitHub.** Pagination, halving of capped windows, the rate limit read from the API and waited out with an injected clock (no real sleeps), a second run making zero requests, a missing token failing before any network call, and the token never appearing in captured logs.
- **Shared infrastructure.** All database tests use the throwaway-database helper; there are no new raw skips; the no-skip rule of `make test` and CI stays.
- **Not automated, recorded as evidence.** Real-corpus steps (tag pin, walk and parse run, audit, benchmark, yield probe, freeze, quality gate, run launch) record the command, the tag, the counts and an output hash in the sprint Results. The real-tag walk equals `go/packages` check is a command whose exit code is the proof.
- **Prior art.** The S0 store integration tests, the HTTP-fake tests for the embed and LLM clients, and the fetch-repo tests.

## Out of Scope

- Call resolution, edges, the VTA graph, offline loadability of `go/packages` (S2; S1 only adds the file-set check).
- Chunks, `search_tsv` population, embeddings, file- and package-level summaries.
- Leakage filtering and choosing evaluation queries (S4).
- Changing the slice or the tag after the freeze and the pin.
- Migrations after 00003.
- Pushing the branch or opening the draft PR (an owner decision on the sign-off checklist).
- Starting or supervising the full overnight run (the author starts it).
- Tuning the summary prompt beyond the quality-gate loop.
- S0 review findings other than the throwaway-database follow-up: the nomic context-size caveat (now an S3 entry checklist item), the template mutation demonstration, a hash-pin test, unit-norm bounds, the Qwen3 prefix superseding decision, the domain-doc rewrite.
- Anything that uses an embedding model.

## Further Notes

- **Facts at spec time (2026-10-10).** Local Go is go1.27.1; the latest stable Kubernetes tag listed upstream is `v1.37.0` (the pin task applies the go-directive rule and records the choice). Docker and Postgres are up with all tables empty; Ollama has `qwen2.5-coder:7b`; `actionlint` and `act` are not installed (hence the pinned `go run`); `data/` is gitignored and empty; `ci.yml` sets no global `CGO_ENABLED`. The `codesearch` database role is a superuser, so the test helper can create databases.
- **Owner steps.** Put a read-only `GITHUB_TOKEN` in `.env` before T12; do the freeze (T13); grade the 30 summaries and start the run (T14); decide on the draft-PR push and merging S0 and S1 at sign-off.
- **Assumptions I made where the owner has not decided.** `slice.sha256` lives next to `slice.go` as the machine-readable copy of the recorded hash (mirrors `eval/queries.test.sha256`); S1 sub-tasks that need Ollama (T09 live test, T10, T11, T14) require Ollama running and the 7B model pulled; the benchmark and the quality-gate sample share one sampler built in T10.
- **Risks.** Unobserved CI for the CGO change; `go/packages` possibly unable to load client-go (Q-05) which stops the freeze for the owner; mining yield below 30; a summary prompt that fails the gate twice.
- Tickets: `issues/01` to `issues/15`, each with a Blocked-by line.
