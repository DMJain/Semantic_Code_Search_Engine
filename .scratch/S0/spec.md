# S0 - Setup verification & fixes

Status: ready-for-agent

Task-level detail (acceptance commands, evidence to record) lives in the plan sprint file `docs/plan/sprints/S0-setup-verification.md` and in the tickets under `issues/`. This spec is the product-level view.

## Problem Statement

The project skeleton for Kubernetes semantic code search was built before the plan existed, and none of it has been trusted yet. As the developer, I cannot tell whether the database migrations work from a clean start, because they were only observed as no-ops on an existing database. The Postgres integration test never runs: locally it silently skips because no test database is configured, and in CI it was never wired up at all. The embedding prompt prefixes were written from memory, and a wrong prefix silently degrades retrieval without any error. A dropped third embedding model is still in the registry. The doctor health command has only been exercised with Ollama missing, never with it healthy. Stale placeholder docs create two sources of truth. Building the real pipeline on top of this would hide bugs under cached or skipped tests.

## Solution

Make the skeleton trustworthy before anything is built on it. After S0: tests always run uncached, and the Postgres integration test visibly passes locally and in CI. Migrations are proven from a fresh volume, including a down and up cycle. The full-text search column is shaped for identifier splitting from Go. The two supported embedding models have prompt templates verified against their official model cards, with evidence and hashes recorded in the decision log. A live probe confirms each pulled embedding model returns well-formed unit-norm vectors of the expected size. Doctor is shown passing with Ollama running and failing helpfully with it stopped. Stale docs are removed. One recorded sign-off run, with a green CI run on a draft pull request, proves it all.

## User Stories

1. As the developer, I want `make test` to bypass the test cache, so that a cached pass can never hide a flaky integration test.
2. As the developer, I want the Postgres integration test to run during `make test` without me exporting variables by hand, so that it is part of the normal loop.
3. As the developer, I want `make test` to fail if the integration test is skipped, so that a missing configuration is loud rather than silent.
4. As the developer, I want the test database URL to come from my environment file, so that no connection string is hard-coded in the build recipe.
5. As the developer, I want a single command to rebuild the database from an empty volume, so that I can prove migrations work from scratch.
6. As the developer, I want a command that refuses to wipe the database if any table holds data, so that I cannot lose work by accident.
7. As the developer, I want a one-step migrate-down and a migrate-up command, so that I can test rollback by hand.
8. As the developer, I want the integration test to run migrate Up, Down and Up in a throwaway database, so that rollback is tested without touching my dev data.
9. As the developer, I want the full-text search column on chunks to be an ordinary column, so that the indexer can fill it with identifier-split tokens.
10. As the developer, I want the new migration to be reversible and to leave the first migration untouched, so that migration history stays honest.
11. As the developer, I want a test proving the search column is not a generated column and can be matched by a text query, so that a regression cannot slip back in.
12. As the developer, I want CI to start a Postgres with the vector extension, so that the integration test runs on every push and pull request.
13. As the developer, I want CI to fail if any store test is skipped, so that the integration test cannot quietly stop running.
14. As the developer, I want stale comments saying the test skips in CI removed, so that the code does not lie.
15. As the developer, I want the embedding registry to contain exactly the two supported models, so that nothing selectable is unsupported.
16. As the developer, I want all traces of the dropped third model removed from code, config examples and doctor text, so that nobody configures it by mistake.
17. As the developer, I want each model's query and document prefix checked against its official model card, so that retrieval quality is not silently degraded.
18. As the developer, I want one fixed task instruction pinned for the Qwen3 query format, so that every embedding run is reproducible.
19. As the developer, I want a defined rule for how a document title joins its text, so that document embeddings are deterministic.
20. As the developer, I want exact-byte render tests per model, so that a one-character change to a template is caught.
21. As the developer, I want the card URL, retrieval date, exact prefixes, native dimension, Matryoshka support, maximum context and embedding context size recorded per model, so that later sprints can rely on them.
22. As the developer, I want a hash of each rendered template recorded, so that a later change to a template is detectable.
23. As the developer, I want a mismatch between the plan and the model card recorded as a new decision that supersedes the old one, so that the decision log stays append-only and traceable.
24. As the developer, I want a probe command that tests every pulled embedding model, so that I know each one works against the real runtime.
25. As the developer, I want the probe to check for NaN or infinite values, correct vector length and unit norm, so that bad embeddings are caught before indexing.
26. As the developer, I want a wrong dimension to fail and print both the expected and actual sizes, so that I can diagnose it immediately.
27. As the developer, I want an unpulled model to be skipped with the pull command shown, so that I know how to fix it.
28. As the developer, I want a skipped model to be an error only when it is the configured embedding model, so that an optional model does not break the run.
29. As the developer, I want a check that sends the same generation request twice and prints both prompt token counts, so that I learn whether prompt caching affects counts.
30. As the developer, I want a check that sends oversized input to each embedding model and reports whether it errors, so that the truncation guard is based on measured behavior.
31. As the developer, I want the exact Ollama model IDs recorded, so that I can tell later if a model changed under me.
32. As the developer, I want a decision saying any model ID change before final evaluation means re-running affected configurations, so that results never mix model versions.
33. As the developer, I want doctor to pass every check with Ollama running, so that I have a known-good baseline.
34. As the developer, I want doctor to fail with Ollama stopped, name the failing check, print the fix message and still report the Postgres checks, so that failures are actionable and independent.
35. As the developer, I want Ollama restored and re-verified after the failure test, so that my machine is left in a working state.
36. As the developer, I want the project instructions to point at the glossary and decision log in the plan folder, so that agents read one source of truth.
37. As the developer, I want stale placeholder docs and an empty query file deleted and links fixed, so that nothing points at dead files.
38. As the developer, I want a check that project instruction files contain no auto-loading imports, so that context is only read on demand.
39. As the developer, I want one recorded sign-off run of every verification command, so that "S0 done" is evidence, not a feeling.
40. As the reviewer, I want work on a branch with one commit per ticket and a draft pull request, so that I can review each change and merge myself.
41. As the reviewer, I want CI proof (the test marked PASS, not SKIP, in the run log), so that I can trust the pipeline.
42. As a future agent, I want each ticket to state dependencies, acceptance and a report format, so that a fresh session can do one ticket and one diff.
43. As a future agent, I want exact evidence formats for decision outcomes, so that I append to the log consistently.
44. As the developer, I want destructive or outward-facing steps (wiping the database, stopping Ollama, pushing) guarded and confirmed, so that nothing irreversible happens unseen.

## Implementation Decisions

- **Test runner contract.** The project test command runs uncached and verbose, requires the test database URL from the environment file (documented in the environment example), and fails on an empty URL or any skipped test. The URL defaults to the same database as the dev URL; the migration test isolates itself by using a throwaway database for rollback cycles.
- **Database tooling.** Add a reset command (volume wipe, restart, migrate), a one-step migrate-down, and a migrate-up alias of the existing migrate command. The migration CLI gains a down mode if it lacks one. The goose binary is not assumed on PATH; everything goes through the project's own CLI. The reset command is preceded by an emptiness check that stops if any table other than the migration-version table holds rows.
- **Schema change.** A second migration replaces the generated full-text column on chunks with a plain tsvector column (not null, default empty) plus its GIN index. Its down migration restores the first migration's generated column and index. The first migration is never edited. Population of the column is a later sprint (chunk stage).
- **CI.** The test job gets a Postgres-with-vector service container matching the local compose credentials, a health check, and the test database URL. It runs tests uncached and adds a step that fails if the store tests report a skip. Stale comments about skipping are removed.
- **Embedding registry.** Exactly two models: the Qwen3 0.6B embedder and nomic-embed-text. The dropped third model is removed everywhere outside the plan folder. Prompt templates are verified against the primary source (upstream model cards on Hugging Face), with Ollama library pages as secondary evidence.
- **Qwen3 query format.** Verified against the card; the instruction text is one pinned sentence. The current template has a space after "Query:" that may not match the card; the card decides. Documents are unprefixed.
- **Nomic format.** Query and document prefixes verified against the card.
- **Document title rule.** A single stated rule for joining title and text, chosen consistently with the embed stage description in the architecture doc, and recorded in the decision log.
- **Per-model facts recorded.** Card URL, retrieval date, quoted prefixes, native dimension, Matryoshka yes/no, max context, embedding context size (4096 unless the card maximum is lower, in which case the chunk limit drops and that is recorded).
- **Decision log discipline.** Append-only. Outcomes are added under the existing entry as "Outcome (ticket, date, evidence)". A changed decision becomes a new entry that supersedes the old, and the old entry is marked superseded. The plan's glossary and decision log are the project glossary and ADR log; no separate context file or ADR folder is created.
- **Doctor additions.** A probe-all mode runs the vector-quality probe for every pulled registry model on three fixed strings and prints one line per model with PASS, FAIL or SKIP. Exit is non-zero on any FAIL or when the configured embedding model is skipped. An Ollama-checks mode prints the two generation prompt-token counts for an identical request, and whether each embedding model errors on over-length input with truncation disabled.
- **Model identity.** Ollama IDs and sizes for the three models are recorded at S0. A new decision states that an ID change before final evaluation forces re-running affected development configurations (re-embedding for an embedder change), and results never mix IDs.
- **Doctor acceptance.** Healthy state: all eight checks pass. Stopped-Ollama state: non-zero exit, failing check named, fix message printed, Postgres checks still reported. The stop and restart procedure depends on how Ollama is installed (Homebrew service versus desktop app) and is detected first.
- **Docs housekeeping.** The agent instruction file's domain-docs line and the domain-docs guide point at the plan glossary and decision log. Placeholder architecture doc and empty eval query file are deleted and links fixed. The no-auto-import check is recorded in the decision log.
- **Process.** Work on one branch. One commit per ticket, with the plan-doc edits as the first commit. Agents push only when CI proof is needed, after showing the owner the workflow triggers and the GitHub auth status. A draft pull request triggers CI. Agents never push to or merge into main.
- **Execution order.** 01, 02, 03, 05, 09, (06 human), 07, 08, 04, 10. Pulling the third Ollama model is a human step with no ticket.

## Testing Decisions

- **What makes a good test here.** Assert external behavior only: rows and columns visible in a real database, bytes sent to a fake Ollama, stdout and exit code of the doctor command, exact rendered prompt strings. Do not assert private helper behavior. Mutation check: a one-character change to any template must make a test fail.
- **Seam 1 - migrations against real Postgres.** The existing store integration test, run against the configured test database. Covers fresh-state tables and extensions, Up/Down/Up in a throwaway database, and the search column shape and text match.
- **Seam 2 - embedder public API against a fake Ollama.** Drive query and document embedding through the public interface with a test HTTP server capturing request bodies, plus a test of the known-models list. Covers the exact prompt bytes per model, with and without a title.
- **Seam 3 - doctor command entry against a fake Ollama.** Run the command with the new modes and assert stdout and exit code for: good vector, wrong dimension, NaN vector, over-length input error, two counts printed, Ollama unreachable with the fix message.
- **No automated seam** for docs housekeeping and the sign-off run; they are verified by the grep and command checks in their acceptance lines. Live-Ollama results (probe table, doctor transcripts, model IDs) are recorded evidence, not automated tests.
- **Prior art.** The existing HTTP-fake tests for the embed and LLM clients, and the existing store integration test.

## Out of Scope

- New features, the real pipeline stages, and populating the search column (later sprints).
- Ollama in CI.
- Embedding quality comparison between models (evaluation sprint).
- Tuning Ollama.
- A race-detector test target and other new test targets.
- The final README.
- Creating a root context file or an ADR folder; the plan's glossary and decision log serve that role.

## Further Notes

- **Assumptions I made where the owner has not decided yet** (change any of them): the ticket for the human model-pull step is omitted, leaving a numbering gap; the test URL defaults to the dev database; `make test` is strict about skips; the title-join rule is chosen by the implementing agent and recorded; the two new decisions (process, model IDs) are written by the first and the live-probe tickets respectively; the scratch folder is committed to the branch in its own commit.
- **Facts at spec time (2026-10-10):** Docker and Postgres 17.11 (vector 0.8.7, trigram 1.6) are up with migrations 0 and 1 applied and all tables empty; Ollama 0.40.1 has the Qwen3 embedder and the 7B coder model but not nomic-embed-text; the goose CLI is not installed; no test database URL is configured anywhere; there is no CLAUDE.md, only AGENTS.md; the registry still holds the third model.
- **Guards:** wiping the volume requires the emptiness check; stopping Ollama requires install-method detection and a restart check; pushing requires the owner's go-ahead on the workflow triggers and auth status.
- Tickets: `issues/01` to `issues/10` (no 06), each with a Depends-on line.
