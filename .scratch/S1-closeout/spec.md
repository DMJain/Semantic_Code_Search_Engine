# S1 close-out - review findings

Status: ready-for-agent

Origin: two-axis code review (Standards and Spec) of every S1 commit since the pre-S1 baseline (`42661eb`), run on 2026-10-12 while the overnight summarize run (S1-16) was in progress. This spec turns the findings into work that must land, or be explicitly decided, before S1 is signed off. It does not reopen the frozen slice (D-070) or the summary prompt version in use by the live run.

## Problem Statement

As the developer, I am about to close S1, but a review found places where the delivered code or its evidence does not match what the S1 spec promised, and places where the code will make S2 to S8 harder to change.

Correctness and evidence gaps:

- The mining-yield probe counts a merged pull request even when it also touches non-Go files outside the candidate slice, so the yield figure recorded in D-070 (150) may be overstated. The frozen slice was justified by that figure (need at least 30 candidates in tiers 1 and 2).
- The probe's search selects pull requests that link an issue, which is not the same as pull requests that closed one.
- The summarize status reports an ETA from a live 30-second sample, not from the last 100 completions that the spec promised, and gives no ETA at all when nothing is pending at the first reading. A related bug (remaining counted from pending rows only) was already fixed and written up.
- The pause command can signal its own shell, its make parent or the keep-awake wrapper, and reports success whenever anything matched. Pause is how the owner stops a night-long run safely.
- A summary row marked failed is never refreshed when the symbol's input hash has changed, unless a retry flag is passed, which contradicts "a summary is fresh only when its input hash matches".
- When a symbol moves between files and the old file is parsed first, the symbol and its summary are deleted and the summary is lost.
- The walk-vs-type-checker check compares stored in-slice rows (not a fresh walk) with the type-checker file set, so it can pass on a stale files table; it also still loads a root that was dropped from the slice.
- The live summarize test skips when no model server is configured, and CI configures none. The strict test target fails on any skip, so a remote CI run would fail, and the "no new raw skips" rule is broken.
- Script linting is promised "skipped when the tool is absent, and Results states that", but no such handling exists.
- The stratified sample still assumes four slice roots while the frozen slice has three.

Maintainability gaps:

- The rule for which functions are eligible for summarization (in the slice, kind function or method, body of at least 3 lines, D-014) is written in five places with a bare number. Changing it means five edits and a silent divergence risk.
- The same small helpers and shapes are copied between commands and packages (source-line reading, empty-to-null, command dispatch boilerplate, seeded sampling).
- Summary status and level are raw strings across SQL and Go; the summarize runner type carries unrelated reasons to change; a config alias adds a layer with no behavior; package comments and ticket identifiers do not follow the repo's conventions.

Scope that was not asked for exists (a gate command, a workspace-mode fallback, a retry on rate limiting, a standalone bug note) and has not been explicitly accepted or documented.

## Solution

Fix the correctness findings with behavior-level tests, re-derive the recorded mining yield under the corrected filter and record the outcome, make the CI skip rule hold without a model server, make pause safe, and tighten the type-check comparison. Consolidate the duplicated eligibility rule and helpers behind single definitions without changing behavior. Decide each unrequested feature (keep and document, or remove) and record it. Then S1 can be signed off with the sign-off evidence consistent with the spec.

The live overnight run must not be disturbed: no change here may alter the prompt version, the input-hash computation or the already-written rows' meaning. Changes that touch the runner are made with the run paused by the owner and take effect on resume.

## User Stories

1. As the developer, I want the mining probe to count a pull request only when every file it changes is a non-test, non-generated Go file inside the candidate slice, so that the yield figure is not overstated.
2. As the developer, I want pull requests that change any non-Go file (workflow, docs, manifests) rejected by the probe, so that the "one to five files all inside the slice" rule is applied as written.
3. As the developer, I want the probe's rejection reasons counted separately (non-Go file, test file, generated file, outside slice, too many files), so that I can see why candidates drop out.
4. As the developer, I want the probe to select merged pull requests that closed an issue rather than merely linked one, so that candidate queries have the intended issue-to-change relationship.
5. As the developer, I want the mining yield re-run under the corrected filter and its new figure and date recorded in a new append-only decision entry, so that the frozen-slice rationale rests on a true number.
6. As the owner, I want a stop-and-ask outcome if the corrected yield is below 30 candidates in tiers 1 and 2, so that I decide whether to extend the mining window instead of an agent changing the frozen slice.
7. As the developer, I want the frozen slice's hash and file list left untouched by this work, so that the evaluation scope stays fixed (D-005, D-070).
8. As the developer, I want the status command's ETA derived from the last 100 completions of the current prompt version, so that the estimate follows the actual recent rate.
9. As the developer, I want the status command to show an ETA of "unknown" with the reason when fewer than 2 completions exist, instead of a misleading value or none, so that I am not misled.
10. As the developer, I want completion times stored per summary row (a nullable completion timestamp added by a new migration), so that the rate can be computed without a live sample.
11. As the developer, I want status to count only the current prompt version, so that rows from a failed gate version never distort progress.
12. As the developer, I want status to return immediately instead of sleeping for a sample window, so that it is cheap to call from scripts and monitors.
13. As the owner, I want `make summarize-pause` to signal only the real summarize process, never the shell running the recipe, make or the keep-awake wrapper, so that pausing cannot kill the wrong thing.
14. As the owner, I want pause to report precisely which process it signalled and to say clearly when nothing was running, so that I can trust the message.
15. As the owner, I want pause to wait until the process has exited (or report that it has not) and to confirm that no row is left half-written, so that resume is safe.
16. As the developer, I want a failed summary whose input hash has changed to be treated as stale and re-attempted on the next run, so that a code change is never hidden behind an old failure.
17. As the developer, I want a failed summary whose input hash is unchanged to stay skipped unless I ask for a retry, so that persistently failing symbols do not loop.
18. As the developer, I want a symbol that moves between files to keep its identity and its summary regardless of which file is parsed first, so that a refactor-sized change does not discard hours of summarization.
19. As the developer, I want a symbol that is genuinely deleted to lose its summary, so that no orphaned summary survives.
20. As the developer, I want the walk-vs-type-checker command to compute a fresh walk of the pinned tree with the corpus build context and compare it with the type-checker file set, so that a stale files table cannot make it pass.
21. As the developer, I want that check to load only the roots in the frozen slice, so that it does not load, or fail on, a root that was dropped.
22. As the developer, I want the check to print the symmetric difference of the two file sets (limited to a readable count) and exit non-zero on any difference, so that a failure is actionable.
23. As the developer, I want the workspace-mode fallback of the check either documented as an accepted decision or removed, so that the behavior matches an agreed rule rather than an accident.
24. As a CI maintainer, I want `make test` to pass in CI with no model server, so that remote CI does not fail on a skip.
25. As a CI maintainer, I want the live-model test to run only under an explicit opt-in target (not as a skipped test in the default run), so that the "no raw skips" rule holds without losing the live check.
26. As the developer, I want the live-model target to fail loudly when the model server is unreachable, so that it can never pass by skipping.
27. As the developer, I want the shell-script lint step to run when the tool is installed and to print an explicit "skipped: not installed" line otherwise, so that the sign-off Results can state which happened.
28. As the developer, I want `make ci-local` to record whether the script lint ran, so that the sign-off checklist reflects it.
29. As the developer, I want the sample stratification to take its roots from the frozen slice definition instead of a hard-coded list, so that it stays correct if the slice changes shape.
30. As the developer, I want the eligibility rule defined once (in the slice, kind function or method, body line count at or above a named threshold) and used by the runner, the benchmark skipped-count, the audit and the gate, so that a change is one edit.
31. As the developer, I want the eligibility threshold to be a named constant referencing D-014, so that the number has a meaning in code.
32. As the developer, I want the SQL form of the rule and the Go form of the rule to be tested against the same fixtures and to agree, so that they cannot drift.
33. As the developer, I want one function for reading a source line range from the pinned tree, used by the prompt builder, the gate and the audit, so that off-by-one fixes land once.
34. As the developer, I want one shared empty-string-to-null helper, so that two copies cannot diverge.
35. As the developer, I want one shared seeded-sampling helper used by the audit, the gate, the symbol sampler and the file sampler, so that "seed 3" means the same thing everywhere.
36. As the developer, I want the command-dispatch boilerplate (command table, usage, flag-set creation) shared by the indexer and eval commands, so that a new subcommand is one entry.
37. As the developer, I want summary status and level represented as named values (one definition used by SQL parameters and Go comparisons), so that a typo cannot silently match nothing.
38. As the developer, I want the summarize runner's run loop, status reporting, benchmark and sampling separated into units that each change for one reason, so that later sprints (S5 hierarchical summaries) can extend one without touching the rest.
39. As the developer, I want the pointless Ollama config alias removed (callers use the one loader), so that there is no layer that only delegates.
40. As the developer, I want test-only hooks in the GitHub client (clock, options, base URL) kept but noted as test seams in the package comment, so that they are not mistaken for speculative features.
41. As the developer, I want package comments to live in a `doc.go` for the walk and GitHub packages, as in the other packages, so that the convention is uniform.
42. As the developer, I want ticket identifiers removed from code and comments (they stay in docs), so that code does not point at closed tickets.
43. As the owner, I want each unrequested feature (the one-off gate command, the workspace fallback, the retry-once on rate limiting, the standalone ETA bug note) to be either accepted with a short decision entry or removed, so that scope is honest.
44. As the owner, I want the gate command either moved out of the production indexer binary into a tools command or kept with a decision entry saying why, so that one-off tools do not pile up in the shipped command.
45. As the owner, I want the S1 sign-off checklist updated with the final list of deviations from the original spec and why each is accepted, so that S1 closes with an accurate record.
46. As the owner, I want a final full local CI run (`make ci-local`) and a status reading showing the overnight run still progressing after these changes, so that the close-out is evidenced and the run is not harmed.
47. As the owner, I want these changes to take effect on the live run only through an owner-controlled pause and resume, so that the run is never altered under me.
48. As the developer, I want existing summary rows and their input hashes to remain valid after the refactor, so that no completed work is redone.
49. As the developer, I want the new migration to be reversible and to apply cleanly on the database that already holds the run's rows, so that I can upgrade in place.
50. As the developer, I want each fix in its own commit with the review finding it addresses named in the message, so that the review trail is auditable.

## Implementation Decisions

Grouped by area. Each is a decision for the implementing agent; none of them reopens a recorded decision.

Mining probe

- Candidate rule is applied as the S1 spec wrote it: every changed file must be a non-test, non-generated Go file inside the candidate slice, and the count of files is one to five. Any other file type rejects the pull request. Rejection reasons are counted and printed.
- The search criterion changes from "links an issue" to "closed an issue" (the closing-reference relation as exposed by the hosting API). If the API cannot express that directly, the agent filters on the pull request's closing-issue references after fetching and documents this.
- The probe re-runs against the cached client and the new figure is recorded in a new append-only decision entry that references D-070 and leaves it unedited. If the new figure is under 30 in tiers 1 plus 2, work stops and asks the owner (story 6); the slice is not changed by an agent.

Summarize status and run

- A nullable per-row completion timestamp is added by a new migration (reversible). The runner sets it when a row becomes done. Rows already done get no timestamp; the ETA uses only rows that have one, and says so when there are fewer than needed. The rate is computed from the most recent 100 timestamped completions of the current prompt version.
- Status no longer sleeps for a sample window. When the rate cannot be computed it prints "ETA unknown" and the reason.
- Freshness rule: a row (any status) is current only when its stored input hash equals the current hash. A failed row with a changed hash becomes pending again; a failed row with an unchanged hash stays skipped unless the retry flag is given.
- Symbol identity across files: parsing writes new and changed symbols first and removes symbols that are absent from the whole parse afterwards (by stable identity), not per file as each file is parsed. A summary follows its symbol; a deleted symbol's summary is removed with it.
- Eligibility is one definition. The Go form is a function with a named threshold; the SQL form is generated from, or tested against, the same threshold. The runner, benchmark, audit and gate use it.
- Sampling takes its roots from the slice definition. One seeded-sample helper is shared.
- The runner is split so the run loop, status, benchmark and sampling are separate units. This is a pure refactor; row contents and hashes are unchanged.
- Status and level become named values; call sites pass them as parameters.

Pause and run tooling

- Pause targets the summarize process by an exact, unambiguous identity (for example a pid file written by the run, or an exact-match process lookup that cannot match the recipe's own shell, make or the keep-awake wrapper). It reports the pid it signalled, waits for exit with a timeout, and prints a distinct message when nothing was running or when the process did not exit.
- The keep-awake wrapper is not signalled by pause; it exits when its child exits.

Walk-vs-type-checker check

- The check runs a fresh walk of the pinned tree (same exclusion rules, corpus build context) and compares it with the type-checker's file set loaded for the slice's roots only. It no longer reads stored in-slice rows as the walk side.
- The workspace-mode fallback is decided by the owner under story 23. Default recommendation: keep it only if it is needed to load the pinned tag, recorded in a decision entry referencing Q-05; otherwise remove it.

CI and test strictness

- The live-model test moves behind an explicit opt-in make target that is not part of `make test` or `make ci-local`; that target fails (not skips) when the model server is unreachable.
- Script lint: if the tool is installed it runs; if not, a single explicit line says it was skipped, and `ci-local` output carries that line so Results can quote it.

Shared helpers and structure

- One source-range reader, one empty-to-null helper, one command-dispatch helper shared by the indexer and eval binaries, one seeded-sample helper.
- The Ollama config alias is removed. Package comments for the walk and GitHub packages move to `doc.go`. Ticket identifiers are removed from code and comments.

Unrequested scope (decide, then record)

- Gate command: owner picks (a) keep in the indexer with a decision entry, or (b) move to a tools command. Default recommendation (b) because it is a one-off.
- Workspace fallback: see above.
- Retry once on rate limiting in the shared GitHub client: default recommendation is keep and document, because the mining probe and later use depend on it; a decision entry records it.
- The standalone ETA bug note: fold its content into the Results section of the S1 sprint doc and remove the loose file.

Sign-off

- The S1 sprint doc's Results lists every deviation from the original spec (including the dropped fourth root and the accepted scope items) and states how CI was checked (`make ci-local` only; nothing pushed, D-069).

## Testing Decisions

A good test here asserts external behavior: rows in the database, command output and exit status, the contents of files a command writes. It does not assert internal structure, private helper names or call order. Refactors (eligibility, helpers, runner split, named status values) are protected by the existing behavior tests staying green without edits, which is the evidence that behavior did not change.

Seams (existing ones preferred, highest first):

1. `make ci-local`, the single end-to-end seam: it runs vet, lint, the strict uncached test run against a throwaway database, the workflow lint and the script lint, and must pass at the end. This is the sign-off seam.
2. The summarize package's public run, status and sample behaviors against a throwaway database with a fake model server (prior art: the existing run, status and interrupted-run tests). New behaviors tested here: failed-row refresh by hash, status ETA from timestamps, symbol move keeps its summary, deleted symbol loses it, eligibility agreement between the Go and SQL forms.
3. The parse command's behavior on a small fixture tree (prior art: the existing parse tests and fixtures): symbol moved between files in either parse order.
4. The mining probe against recorded API responses (prior art: the existing probe tests with the cached client): a pull request with a non-Go file is rejected; a pull request that only links an issue is rejected; rejection counts are printed.
5. The walk-vs-type-checker check against the existing fixture module (prior art: the existing fixture module and its tests): a stale files table does not make it pass; a root outside the slice is not loaded; a seeded difference is reported.
6. The pause recipe, tested with a small stand-in long-running process: it signals only that process, not the shell or a wrapper whose command line contains the same text; it reports when nothing is running.

I propose these six seams; if the owner wants fewer, 2 to 5 collapse naturally into "package behavior tests" with seam 1 as the only cross-cutting one.

Each fix starts with a failing test at one of these seams and ends with it passing. The tests added for the pause recipe and the opt-in live target must not reduce the strict no-skip rule.

## Out of Scope

- Reopening the frozen slice, its hash, or the composition of its roots (D-005, D-070), except through the owner's decision under story 6.
- Changing the summary prompt, the prompt version or the quality-gate verdicts.
- Supervising the overnight run to completion (it continues under S1-16 and may span into S2 to S4).
- Function-level call graph work (S2) and any file or package level summaries.
- Pushing, opening pull requests, or any remote action (AGENTS.md, D-069).
- Performance tuning of summarization throughput.

## Further Notes

- Findings are from a review that did not run the tests and only skimmed some code (the GitHub client and the audit and benchmark code). Each ticket should begin by reproducing its finding with a failing test or command output; a finding that does not reproduce is closed with a note, not "fixed".
- Highest risk to the project: the mining yield correction (story 5). If the corrected figure is below the threshold, the evaluation set's size is affected and the owner must choose between extending the mining window and other options listed in D-005.
- Second risk: touching the runner while the overnight run is active. All runner changes are done with the run paused by the owner; the agent never starts, stops or signals the run itself.
- Standards review found no hard violations. All maintainability items are judgement calls from the Fowler smell baseline; where a documented repo convention (package comments in `doc.go`) applies, it wins.
- Glossary terms used: slice, frozen slice, eligible function, prompt version, input hash, stale, fresh.
