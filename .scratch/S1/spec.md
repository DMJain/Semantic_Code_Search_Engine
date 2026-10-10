# S1 - Corpus & symbols

Status: ready-for-agent

## Problem Statement

As the developer of the Kubernetes semantic code search engine, I have no corpus yet. There is no pinned Kubernetes tag, no list of indexed files, no symbols and no function summaries, and every later stage (call graph, chunks, embeddings, the evaluation) stands on them.

I cannot yet tell which files are in scope, whether a tree-sitter parse of Kubernetes gives correct symbols, how fast the local model summarizes, or whether enough merged pull requests exist inside the scope to build 25 semantic evaluation queries. The scope becomes the evaluation scope and can never change after it is frozen, so a wrong guess about its size, its yield, or whether the walk and the type-checked graph see the same files would invalidate everything built afterwards.

The function-summary run is the longest job in the project and sits on the critical path. Starting it with a poor prompt, with summaries that can be orphaned from their symbols, or with a pause that leaves half-written rows would waste a large part of the compute budget.

The build setup also has two traps. CI cannot be observed without pushing, and the parser needs a C toolchain: a globally enabled C toolchain would silently change which files the corpus walk accepts, because the corpus build context is Linux, amd64, cgo off. Finally, database tests currently share the dev database, so parallel packages could collide and a cleanup could wipe real rows.

## Solution

Pin one Kubernetes tag and build, in order: a walk that decides the filtered file set, a parser that stores functions, methods, structs, interfaces and raw call names with stable ids, and an audit of both on the real tag. Before the slice is frozen, prove by a command that the walk and `go/packages` see exactly the same files under the same build context.

Measure summarization throughput and probe how many evaluation queries can be mined from the candidate slice. Then the author freezes the slice, recording a hash of its sorted file list in a new, append-only decision entry.

Build a pausable, resumable summarize command and check summary quality on a seeded sample graded by the author against a written rubric. The author then starts the overnight run in their own terminal. S1 is done when the slice is frozen and the author-started run is verifiably progressing, not when the run finishes.

Set up first a local mirror of CI (`ci-local`) and a CGO-safe CI configuration so the parser can be added without touching the corpus build context. Every database test gets its own throwaway database and fails instead of skipping when the database is required.

## User Stories

1. As the developer, I want a `ci-local` command that runs the same steps and environment as the CI workflow against my local Postgres, so that I get CI-equivalent proof without pushing.
2. As the developer, I want `ci-local` to fail when any test is skipped, so that a missing database configuration cannot pass quietly.
3. As the developer, I want the workflow file checked by a pinned linter that needs no installation, so that a typo in the workflow is caught locally and reproducibly.
4. As the developer, I want the linter version pinned and the reason recorded, so that the check does not change under me.
5. As the developer, I want the C toolchain enabled only for building, vetting and testing, so that the parser compiles without a job-wide setting.
6. As the developer, I want a test that sets the C-toolchain variable in the environment and shows the walk's build context still has cgo off, so that the corpus file set cannot drift with my shell.
7. As the developer, I want a test that shows the environment handed to `go/packages` always ends with cgo off, so that the type-checked graph uses the same build context as the walk.
8. As the reviewer, I want the unobserved CI change recorded as a risk with the draft-PR push listed as an owner decision, so that the deferral is explicit.
9. As the developer, I want one Kubernetes tag chosen by a stated rule (latest stable release whose go directive is not newer than my toolchain), so that the choice is reproducible.
10. As the developer, I want the tag, its date, its go directive and its commit recorded in the decision log, so that every label and result refers to one source tree.
11. As the developer, I want the shallow clone to keep the vendor directory on disk but never index it, so that the type-checked graph can load dependencies without polluting the corpus.
12. As the developer, I want fetching the repository twice to be a no-op, so that setup is safe to repeat.
13. As the developer, I want a reversible migration that adds the raw-calls column, the parse marker, the summary failure reason and a uniqueness rule for summaries, so that rollback stays tested.
14. As the developer, I want the database to reject a second summary for the same level, target and prompt version, so that two runs can never double-write.
15. As the developer, I want the walk to apply named exclusion rules and print a count per rule, so that I can see what each rule removed.
16. As the developer, I want files rejected by the Linux, amd64, cgo-off build context counted under their own rule, so that the indexed files match what the compiler loads.
17. As the developer, I want a file that imports C excluded even when my shell enables cgo, so that results are identical on any machine.
18. As the developer, I want files for other operating systems excluded by name and by build constraint, so that only Linux code is indexed.
19. As the developer, I want generated code excluded by naming and by the generated-file header, including generated client code, so that the corpus holds hand-written code only.
20. As the developer, I want walking twice to report zero changes, and vanished files removed from the database, so that the stage is idempotent and the file table never goes stale.
21. As the developer, I want the exclusion rules implemented once and reused by the audit, the file-set check and the mining probe, so that three tools cannot disagree about what is excluded.
22. As the developer, I want functions, methods, structs and interfaces extracted as symbols, so that every searchable unit has a row.
23. As the developer, I want qualified names that preserve pointer versus value receivers, so that a symbol can be found by its full name.
24. As the developer, I want duplicate names in one package (several `init` functions, same names in different files) disambiguated deterministically across files, so that qualified names are globally unique.
25. As the developer, I want grouped type blocks to yield one symbol per type, and aliases and function literals not to be symbols, so that symbol counts mean something.
26. As the developer, I want raw calls stored per function as written, deduplicated and capped, so that summaries can mention what a function calls without waiting for call resolution.
27. As the developer, I want raw calls never to feed the call graph, so that the unresolved text cannot contaminate edge quality.
28. As the developer, I want parsing twice to report zero files to parse, so that the stage is idempotent.
29. As the developer, I want an interrupted parse to finish with the same totals as an uninterrupted one, so that the stage is resumable.
30. As the developer, I want a symbol to keep its id when its file is re-parsed, so that summaries never point at a wrong or a missing symbol.
31. As the developer, I want symbols removed from a file to disappear together with their function summaries, so that no orphan summary survives.
32. As the developer, I want a summary to count as fresh only when its input hash recomputed from the current symbol matches, so that a shifted duplicate name cannot keep a stale summary.
33. As the developer, I want files with parse errors still stored and counted, so that one odd file never blocks the run.
34. As the developer, I want a seeded random audit of 30 stored symbols checked against the source lines and doc comments, so that parse quality is measured on the real tag instead of assumed.
35. As the developer, I want a command that proves the walk's file set equals the file set `go/packages` loads for the four slice roots, so that tree-sitter and the type-checked graph describe the same code.
36. As the developer, I want an unequal result to print both difference lists with totals and exit non-zero, so that I can see exactly what disagrees.
37. As the developer, I want a load failure to stop for me with the exact failure recorded as evidence for the open question, so that I decide how to proceed before the freeze.
38. As the developer, I want one function-summary prompt and a single current prompt-version constant, so that every stage filters on the same version.
39. As the developer, I want an explicit context size in every request to the local model, so that input is never silently truncated by defaults.
40. As the developer, I want over-long requests refused before sending and truncated responses flagged after sending, each recorded with a reason, so that a cut-off input never becomes a silent summary.
41. As the developer, I want functions with bodies under three lines skipped and counted, so that trivial functions do not consume the compute budget.
42. As the developer, I want the summarize command to resume after SIGINT or SIGTERM without partial rows or repeated model calls, so that the long run can be paused for any reason.
43. As the developer, I want unchanged work skipped by input hash, so that reruns cost nothing.
44. As the developer, I want a seeded, stratified sample option that returns the same ids for the same seed, so that the quality gate and the benchmark are reproducible.
45. As the developer, I want measured summarization throughput at concurrency 1 and 2 plus file-level and package-level prompt rates, so that slice size comes from measurement.
46. As the developer, I want the benchmark never to write rows into the summaries table, so that it cannot pollute the real run.
47. As the developer, I want a mining-yield probe over merged GitHub pull requests that splits capped search windows, reads the rate limit from the API and caches every response by query and window, so that I learn whether 25 semantic queries are minable without spending the quota twice.
48. As the developer, I want the probe to report a 12-month yield and an 18-month extension yield counted after every automatable filter, so that the freeze decision uses comparable numbers.
49. As the developer, I want the probe to fail before any network call when the token is missing, and never to print or log the token, so that the secret stays secret.
50. As the developer, I want the probe's cache reused by the later query-mining sprint, so that the same requests are never made twice.
51. As the author, I want to freeze the slice only after the size projection, the yield and the file-set check are all known and passing, so that the evaluation scope is final and sound.
52. As the author, I want the compute-budget rule applied with an ordered drop list, so that the slice shrinks in a predictable, recorded way.
53. As the author, I want the freeze recorded as a new decision entry with the tag, commit, size calculation, eligible-function count, ordered drop list, recomputed yields, file-set check result and the hash of the sorted file list, so that the freeze is auditable and the decision log stays append-only.
54. As the developer, I want a `verify-slice` command that recomputes the hash and fails on any drift, so that later sprints can confirm the scope never moved.
55. As the author, I want to review 30 seeded summaries against a written rubric before the overnight run, with at most 3 bad allowed, so that a poor prompt is caught before it costs compute.
56. As the author, I want the failure counts among the 200 sampled summaries reported separately, so that over-large and truncated inputs are visible before launch.
57. As the author, I want a failed gate to bump the prompt version and redo the gate, and a second failure to stop the agent, so that nobody loops on a bad prompt.
58. As the developer, I want the failed version's function-level summaries deleted after the new gate passes and before any embedding, so that stale-version rows never feed later stages.
59. As the author, I want to start the overnight run myself in my own terminal with a command that keeps the Mac awake, so that I control when the machine is busy.
60. As the developer, I want pause and resume verified by counts (start, wait for a few completions, interrupt, check no partial rows, resume, check progress and no duplicates), so that the check does not depend on elapsed time.
61. As the developer, I want a status command that counts only the current prompt version and shows an estimate, so that old-version rows cannot inflate progress.
62. As the reviewer, I want work on a branch stacked on the S0 branch, with the plan-document commit first and then one commit per ticket, so that I can review each change and merge myself.
63. As the reviewer, I want the S0 follow-up (throwaway-database helper, no-skip test run) landed on the S0 branch before the S1 branch is cut, so that S1 builds on a trustworthy test setup.
64. As the developer, I want every database integration test to run in its own throwaway database that is dropped afterwards and never to touch the dev database, so that tests can run in parallel and cannot wipe real rows.
65. As the developer, I want a missing test database to skip a test locally but fail it when the database is required, so that the default `make test`, `ci-local` and CI can never pass without running the database tests.
66. As the developer, I want a command that removes leftover throwaway databases, so that interrupted runs do not accumulate junk.
67. As a future agent, I want each ticket to name its plan task, its blocking tickets, its acceptance checks and its report format, so that a fresh session can do one ticket and one diff.
68. As a future agent, I want the new terms (candidate slice, drop list, eligible function, raw calls, qualified name, the two yields, quality gate, ci-local) defined in the glossary, so that I use the project's vocabulary.
69. As the author, I want S1 considered done when the slice is frozen with its hash, the gate has passed, my run is progressing across two status readings and pause and resume are verified, so that done does not depend on how long the run takes.

## Implementation Decisions

- **Process.** The S1 branch is stacked on the S0 branch. The S0 follow-up (throwaway-database test helper, widened skip check, decision-log order fix) and the S0 review gaps (template pinning, embedding context size, a real stopped-Ollama run, domain-doc cleanup, re-sign-off) landed on the S0 branch first, and this branch was rebased onto it. The plan-document commit comes first on the S1 branch, then the scratch folder, then one commit per ticket. Agents do not push, and never push to or merge into the main branch. A draft PR for CI proof is an owner decision on the sign-off checklist (D-061, D-062).
- **Tickets.** Sixteen vertical-slice tickets, each naming the plan tasks it covers ("Plan: S1-Tnn"), so references such as the mining client in the later sprints still resolve. Plan task T15 (`ci-local` and CGO-safe CI) is new. T08 is split into the real-tag audit and the file-set check; T14 is split into run tooling and the human quality gate and launch. Tickets 14 (freeze) and 16 (quality gate and launch) are for the human.
- **Blocking edges.** Parse, summarize, benchmark and the freeze form one chain; the file-set check depends only on the walk and can run alongside the parse tickets; the mining probe depends on the tag and the walk (not on the benchmark); the freeze waits for the audit, the file-set check, the benchmark and the mining probe.
- **Execution order.** 01, 02, 03, 04, 05, 06, 07, 09 (any time after 04), 08, 10, 11, 12, 13 (any time after 04, token already in place), 14 (author), 15, 16 (author).
- **Build context versus toolchain CGO (D-062, D-058).** The C toolchain is enabled only on the build, vet and test steps of CI, in `ci-local` and in the Makefile build. The corpus build context is Linux, amd64, cgo off. The walk sets cgo off explicitly on its build context, and the `go/packages` environment always has cgo off appended last, so an inherited value never wins.
- **`ci-local`.** Mirrors the CI workflow's commands and environment against the compose Postgres: vet, one test run over every package with the database required and a failure on any skip, lint, and a workflow lint. The workflow linter runs through a pinned `go run` with the toolchain forced local, with a variable for the workflow file so a scratch copy can be checked. Shellcheck integration is skipped when the tool is absent, and Results states that.
- **Test database isolation (D-065).** Each database test gets a freshly created, migrated, lowercase database whose name carries a fixed prefix and a random suffix, kept within the 63-byte identifier limit, and dropped with force on cleanup. The helper refuses to drop any name without the prefix. An empty test URL skips a test unless the require flag is set, in which case it fails. A cleanup command drops leftovers by prefix. No test writes to the database named in the test URL, and no database test uses a raw skip.
- **Schema.** One new reversible migration adds the raw-calls column on symbols, the parse marker on files, the failure reason on summaries, and a unique rule on summaries over level, target and prompt version. No later migration is added in S1.
- **Corpus and walk.** Shallow clone at the pinned tag, vendor kept on disk, never indexed. Exclusion rules are named predicates in one exported function (vendor, generated, protobuf, test data, test files, generated client-go code, build constraint), with counts per rule. The candidate slice is the file set under the four slice prefixes after exclusions; the slice is that set after the drop list. Upserts are batched in a transaction and vanished paths are deleted.
- **Symbols (D-064).** Functions, methods (receiver kind preserved), structs and interfaces; function literals are not symbols. Qualified names are numbered across the whole package for duplicates, ordered by path and start line. Per file, in one transaction: upsert on qualified name returning ids, delete the file's unseen symbols together with their function summaries, then set the parse marker. Summary freshness is decided by the recomputed input hash, never by target id alone. The reset command is not used while summaries are kept.
- **File-set check (D-066).** A check mode of the call-graph tool compares the walk's file set with the `go/packages` file set for the four slice roots under the identical context, with client-go loaded by import path from the root module, symlinks normalized, and the shared exclusion predicate applied. Exit codes: 0 equal, 1 unequal (both directions, capped listing, totals), 2 load error (recorded as evidence for the open question, then the owner decides). It is re-run on the post-drop-list set before the freeze. Offline loadability and the VTA graph stay in S2.
- **Summarization (D-014, D-015, D-067).** One prompt version constant defined in one place. The prompt carries signature, doc, raw calls and the code cut to a character limit. Every request has an explicit context size. A request estimated above 80 percent of the context is not sent and is recorded as `too_large`; a response whose prompt token count reaches the context minus the output limit is recorded as `truncated`. Selection is in-slice functions and methods with a body of at least three lines. State per row is pending, done or failed with a reason; done rows with an unchanged input hash are skipped; SIGINT and SIGTERM finish the in-flight item and exit 0.
- **Sampling.** A seeded draw stratified across the four slice roots in proportion to eligible counts, with candidates ordered by qualified name before the shuffle, so the same seed gives byte-identical id lists. The benchmark and the quality gate share it.
- **Benchmark.** 200 eligible functions (seed 7) with the production prompt at concurrency 1 then 2, plus 20 stratified file-level and 3 package-level draft prompts. Results go to a results file, never to the summaries table. Throughput is reported as a metric only; no acceptance check depends on wall time.
- **Mining probe.** GitHub REST search in weekly merged-date windows ending at the tag date, halving any window that reaches the 1,000-result cap, reading the search rate limit from the API and waiting on it, caching by query and window. Counts merged PRs that closed an issue and changed one to five non-test non-generated files that are all inside the candidate slice and exist at the tag, reporting the 12-month yield, the 18-month extension yield and the per-prefix contribution. It fails before any network call without a token, never prints the token, and its cache is reused by the later mining sprint.
- **Freeze (D-005, D-063).** The author applies the compute-budget rule to the projected summarization run length, applies the drop list in order if the slice is too big, re-runs the walk, checks that the two yields sum to at least 30 (changing composition within the budget or widening the window if not), and re-runs the file-set check. Then a new entry "Slice frozen (refs D-005)" is appended with the tag and commit, size and calculation, eligible-function count, ordered drop list, both yields recomputed from the cache, the check's exit code and the hash of the sorted file list (repo-root-relative paths, slash separators, one per line, LF-terminated, bytewise sort). D-005 is not edited. The same hash is stored in a machine-readable copy that `verify-slice` reads. No CI hash pin. A re-freeze needs an owner-authorized superseding entry.
- **Quality gate (D-067).** A stratified seeded sample of 200 is summarized and 30 `done` rows (seed 3) are graded by the author: a summary is bad if it states behavior the code lacks, names an absent callee or behavior, restates the signature, contains code or preamble, omits the primary effect or return value, or is empty or cut off, with the first failing criterion recorded. At most 3 bad passes. A fail bumps the prompt version and redoes the gate; a second fail stops for the owner. Verdicts are recorded as one JSON line per summary. Passing gate rows are kept by the full run; a failed version's function-level rows are deleted before any summary embedding.
- **Overnight run tooling.** A run command that builds the binary and wraps it in a keep-awake call at concurrency 1; a pause command that interrupts the process; a status command that counts only the current prompt version with an estimate from the last 100 completions; `verify-slice`. The agent never starts the full run; the author does.
- **Definition of done (D-063).** Slice frozen with its hash recorded, gate passed, the author-started run showing strictly increasing `done` across two status readings, pause and resume verified by counts, `ci-local` green with no skips, plan roadmap updated, and a sign-off checklist listing the draft-PR push and the merge of S0 and S1 as owner decisions.
- **Decision log and glossary.** Six new decisions (D-062 to D-067) are appended in ascending order; the glossary gains candidate slice, drop list, eligible function, raw calls, qualified name, 12-month yield and 18-month extension yield, quality gate and `ci-local`. The S3 entry checklist gains the nomic context-size caveat from the S0 review.

## Testing Decisions

- **What makes a good test here.** Assert external behavior only: files kept and excluded per rule, rows and columns in a real database, bytes sent to a fake Ollama or fake GitHub, exit codes and output of commands. No assertions on private helpers. Golden files are never rewritten by `make test` or CI; regeneration is a manual flag.
- **Seam 1 - walk against a module fixture.** A small committed module tree gives a golden kept-file list and per-rule counts, including negatives (a `_windows.go` file, a file with a Windows build constraint) and a file that imports C. The walk's set equals the `go/packages` set under the identical context, run with the C-toolchain variable set in the environment. An integration test covers idempotency.
- **Seam 2 - parse against golden fixtures and real Postgres.** Goldens for pointer and value receivers, generics, grouped type blocks, closures, method chains and raw calls; duplicate `init` functions numbered across files; id stability after a re-parse with the marker cleared; a removed function leaves no symbol and no summary (the orphan query returns 0); an inserted extra `init` makes the shifted summaries report stale.
- **Seam 3 - summarize against a fake Ollama and real Postgres.** The exact prompt bytes; the context size in every request body; a symbol with a two-line body is skipped and one with three is included; `too_large` with zero HTTP calls; `truncated`; a rerun makes zero calls; SIGINT mid-run leaves no partial rows and resume yields zero duplicates; the same seed gives byte-identical id lists; concurrency 2 under the race detector.
- **Seam 4 - mining probe against a fake GitHub.** Pagination, halving of capped windows, the rate limit read from the API and waited out with an injected clock (no real sleeps), a second run making zero requests, a missing token failing before any network call, and the token never appearing in captured logs.
- **Shared infrastructure.** All database tests use the throwaway-database helper, and its name builder and prefix refusal have their own tests. There are no new raw skips, and the no-skip rule of `make test`, `ci-local` and CI stays.
- **Recorded evidence, not automated tests.** Real-corpus steps (tag pin, walk and parse run, audit, benchmark, yield probe, freeze, quality gate, run launch) record the command, the tag, the counts and an output hash in the sprint's Results. The real-tag file-set check is a command whose exit code is the proof.
- **Prior art.** The S0 store integration tests, the HTTP-fake tests for the embedding and LLM clients, and the fetch-repo tests.

## Out of Scope

- Call resolution, edges, the VTA graph and offline loadability of `go/packages` (S2). S1 only adds the file-set check.
- Chunks, full-text column population, embeddings, and file-level and package-level summaries.
- Leakage filtering and choosing evaluation queries (S4).
- Changing the slice or the tag after the freeze and the pin.
- Migrations after the one S1 adds.
- Pushing the branch or opening the draft PR (an owner decision on the sign-off checklist).
- Starting or supervising the full overnight run (the author starts it).
- Tuning the summary prompt beyond the quality-gate loop.
- Any further S0 work: the S0 review gaps are closed (see the S0 sprint Results); the nomic embedding context size (2048) now has an S3 entry checklist item for the chunk ceiling.
- Anything that uses an embedding model.

## Further Notes

- **Facts at spec time (2026-10-10).** Local Go is 1.27.1; the latest stable Kubernetes tag listed upstream is v1.37.0 (the pin ticket applies the go-directive rule and records the choice). Docker and Postgres are up with all tables empty; Ollama has the 7B coder model; the workflow linter and `act` are not installed, hence the pinned `go run`; the CI workflow sets no job-wide C-toolchain variable; the database role is a superuser, so the test helper can create databases. The read-only GitHub token is already set in the environment file.
- **Network use.** Internet is used for the one-time Kubernetes clone and the one-time, cached GitHub mining probe only. Indexing, parsing and summarization are offline; the project's rule is "no paid or hosted dependencies, indexing offline", not "no network ever" (D-001, D-007).
- **Owner steps.** Do the freeze; grade the 30 summaries; start the overnight run; decide on the draft-PR push and on merging S0 and S1.
- **Assumptions made where the owner had not decided.** The machine-readable copy of the slice hash lives next to the slice definition, mirroring the evaluation freeze file; tickets that need Ollama (the live summarize test, the summarize command, the benchmark, the gate) assume Ollama is running with the 7B model pulled; the benchmark and the quality gate share one sampler built in T10.
- **Risks.** Unobserved CI for the C-toolchain change; `go/packages` possibly unable to load client-go (the open question), which stops the freeze for the owner; mining yield below 30; a summary prompt that fails the gate twice.
- **Where the detail lives.** Task-level commands and acceptance checks are in the plan's S1 sprint file and in the sixteen tickets in this folder's `issues` directory, each with a Blocked-by line. Decisions D-062 to D-067 hold the rationale. The tickets for the S0 gaps are in the S0 scratch folder.
