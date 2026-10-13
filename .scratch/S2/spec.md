# S2 - Call graph

Status: ready-for-agent

Supersedes the task list S2-T01 to S2-T09 in `docs/plan/sprints/S2-call-graph.md`. Where this spec and that file differ, this spec wins; the decisions it rests on are D-011, D-019, D-023 (amended by D-075), D-025, D-058 (amended), D-067, D-071, D-073 to D-077, and the open questions Q-04 and Q-05.

## Problem Statement

As the developer of the Kubernetes semantic code search engine, I have symbols but no call graph. The structural retriever (S5/S6), the chunk headers (top callers and callees, S3) and the structural evaluation labels (D-012) all need to know who calls whom. Today `edges` is empty (0 rows), and the plan for filling it has four problems.

First, there is no honest quality number. A heuristic tree-sitter graph is only useful if I know, per kind of call, how often its edges are right and how many real calls it misses. Without a type-checked ground truth to compare against, "the call graph works" is a guess.

Second, the planned storage vocabulary hides failure. The old plan stored "nothing found" and "external callee" as `name_only` with no callee, which makes a low-confidence guess and an admission of failure look the same to every reader. It also had no place for calls that tree-sitter deliberately cannot resolve, no cap on interface calls that match hundreds of types, no rule for when stored edges go stale when other files change, and no rule for how ground-truth rows coexist with tree-sitter rows in one table.

Third, the ground-truth graph may not be buildable. VTA over the slice and its dependencies must fit in memory (Q-04), and the pinned tag does not load under the vendor layout that earlier docs assumed (D-071), so the load invocation is still unrecorded (D-025).

Fourth, scoring is easy to get subtly wrong. The slice was frozen without client-go (D-070), embeddings and ground truth exist only for the slice (D-029), but the tree-sitter graph covers the whole filtered repo, so most edges point at code that has no ground truth. If those are mixed into precision and recall the numbers mean nothing, and during S1 the delivered work drifted from what was discussed. This spec therefore lists every agreed rule explicitly.

## Solution

Build, in this order of risk, two edge sets in one `edges` table and a report that compares them.

1. **Ground-truth graph (GT).** `callgraph-gt` loads the slice under the workspace load with the fixed build context, builds the type-checked VTA call graph, and stores in-slice edges with `source='gopackages'`. Before anything depends on it, a read-only run measures peak memory and records the working load invocation, closing Q-04 and Q-05 (or stopping for the owner).
2. **Tree-sitter graph.** `indexer graph` re-parses every indexed file (the whole filtered repo, not only the slice), extracts call sites with import aliases and a small per-function type environment, resolves them with five ordered rules, and stores `edges` with `source='treesitter'`. Every call site ends up in exactly one place: a stored edge (exact, name-only, ambiguous or unresolved with a reason), a skip counter (builtin, conversion) or, while the work is incomplete, a deferred counter (counters are printed by `graph --stats` and are not stored). The command is idempotent, resumable and knows when its edges are stale.
3. **Report.** `eval callgraph` compares tree-sitter edges to GT edges on the slice by call kind, with every edge accounted for in a partition that must sum to the total.
4. **Error analysis.** A seeded sample of wrong and missing edges is classified by cause; a fix happens only under a fixed rule, otherwise the limitation is recorded.

S2 is done when both graphs are populated and re-runnable, the report is recorded, D-025 is filled, Q-04 and Q-05 are closed, no `deferred_*` counter remains, no S2-related PENDING remains in the decision log, and the partition assertions pass on both sides with `gt_unmapped` printed.

The work is delivered as vertical slices, run one at a time on `main`, starting with the memory measurement because it is the only step that can need an owner decision.

## User Stories

Ground truth and loading

1. As the developer, I want `callgraph-gt` to load the Kubernetes slice under the workspace load with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=local`, so that the ground truth sees exactly the files the walk indexed.
2. As the developer, I want the working invocation recorded in D-025 starting from the workspace load and citing D-071, so that nobody rediscovers the failing vendor load.
3. As the developer, I want a load with `GOPROXY=off` and `GOFLAGS=-mod=readonly` to be tried, and Q-05 closed only if it succeeds, so that "works offline" is evidence and not assumed.
4. As the developer, I want the existing file-set check to still print `file-set: equal` after the load settings change, so that D-058 and D-067 still hold (876 files, 720 packages, 0 errors at last record).
5. As the developer, I want D-058's wording amended to match the real invocation, so that the decision log does not contradict the code.
6. As the developer, I want a read-only first run that builds SSA and VTA for the slice without touching the database, so that I learn the memory cost before building anything on it.
7. As the developer, I want `GOMEMLIMIT` set and peak resident memory measured with the platform timing tool and recorded in the result metadata as `peak_rss_bytes`, so that Q-04 is closed by a number.
8. As the developer, I want that run to also record packages loaded, SSA function count, VTA edge total and in-slice edge count, so that the cost can be understood and reproduced.
9. As the developer, I want the run to happen with no model server, summary job or embedding job running and no other session using the machine, so that the measurement is not polluted (the summary run is paused with the documented safe pause).
10. As the developer, I want the VTA input to be only the slice functions plus the initial call graph, so that the measurement is the smallest correct computation, not a bigger one.
11. As the developer, I want the run to stop and write an escalation report when peak memory exceeds 16 GiB (which step ran out, the peak, what was tried) and its ticket to become `ready-for-human`, so that a weaker ground truth is never adopted silently.
12. As the developer, I want per-root VTA runs, CHA and RTA to remain rejected unless I decide otherwise, so that the ground-truth quality (D-011, D-012) is not changed by an agent.
13. As the developer, I want the ticket that stores GT edges to say it is blocked until the memory result is resolved, so that nothing is built on an unmeasured computation.
14. As the developer, I want GT edges kept only when caller and callee are both in slice files, so that the ground truth matches the scoring universe.
15. As the developer, I want each SSA function mapped to a symbol's qualified name with pointer receivers preserved, so that GT callers and callees are the same symbols tree-sitter uses.
16. As the developer, I want closures attributed to the enclosing declared function or method, so that GT and tree-sitter attribute calls inside function literals identically.
17. As the developer, I want GT callers with no symbol (package variable initialisers, synthetic `init`, bound-method and thunk wrappers) dropped and counted, so that nothing unmappable silently skews the graph.
18. As the developer, I want generic instantiations mapped to their origin symbol, so that generic code is not lost.
19. As the developer, I want any in-slice GT edge that cannot be mapped to a symbol id counted as `gt_unmapped` and printed, so that mapping loss is visible and never hidden.
20. As the developer, I want GT call kinds taken from the SSA call mode (static function `func`, static method `concrete_method`, invoke `interface_method`, dynamic `func_value`), so that kinds are defined by the type checker.
21. As the developer, I want each GT edge to store the callee's qualified name as the raw text and the call line (0 when unavailable), so that GT rows have the same shape as tree-sitter rows.
22. As the developer, I want GT rows stored with `resolution='exact'` and no unresolved reason, so that the edge table needs no extra vocabulary (D-077).
23. As the developer, I want all GT rows replaced in one transaction on every run and two runs to give identical counts, so that the ground truth is reproducible.
24. As the developer, I want a committed tiny module fixture with an interface, a function value, a concrete method and a closure, so that the four kinds and the attribution rules are tested without the real tree.
25. As the developer, I want GT wall time and peak memory recorded in the sprint Results, so that the cost is documented.

Edge model and storage

26. As the developer, I want `resolution` to have four values (`exact`, `name_only`, `ambiguous`, `unresolved`) and `unresolved_reason` to have five (`external`, `no_match`, `unknown_receiver`, `func_value`, `candidates_capped`), so that a guess and an admission of failure are never confused (D-073).
27. As the developer, I want the database to enforce that a row is `unresolved` exactly when its callee is NULL and exactly when it has a reason, so that the shape cannot drift.
28. As the developer, I want `name_only` always to have a callee, so that readers can trust "has a callee means it points at a real symbol".
29. As the developer, I want a call line and a call kind stored on every edge (kinds `func`, `concrete_method`, `interface_method`, `func_value`, `unknown`), so that scoring by kind and evidence lines are possible.
30. As the developer, I want a unique index over source, caller, raw callee text, call line and callee (NULL treated as zero) and inserts to ignore conflicts, so that two identical calls on one line give one row.
31. As the developer, I want the callee reference to cascade on delete (the caller reference already does), so that removing a symbol never breaks the shape rule (D-076).
32. As the developer, I want existing rows (there are none today) converted before the new constraint is added, so that the migration is safe if data appears first.
33. As the developer, I want the migration reversible and the down/up cycle to leave the table description unchanged, so that it follows the project's migration rules.
34. As the developer, I want the migration numbered 00005 and the later sprint migrations renumbered in the plan, so that the plan matches the migrations that already exist.
35. As the developer, I want every non-evaluation query that exists in S2 (`graph --stats`, `graph --status`) to filter on the tree-sitter source, with a test where one GT row and one tree-sitter row for the same pair leave those outputs unchanged, and the same one-edge criterion added to the S6 structural-retriever acceptance, so that ground truth can never leak into the product (D-077).
36. As a downstream retriever author, I want to follow only edges that have a callee and list unresolved rows separately with their reason, so that unresolved calls are visible and never traversed.

Call-site extraction

37. As the developer, I want every indexed file re-parsed with tree-sitter (not read from raw calls), so that resolution has the local types and import aliases that raw calls lack (D-019).
38. As the developer, I want a per-file import alias map (alias to import path) with dot and blank imports ignored, so that `alias.Name` calls resolve to the right package.
39. As the developer, I want a per-function type environment from the receiver, the parameters, `var x T`, `x := T{...}`, `x := &T{...}`, `x := NewT(...)` where `NewT` is a same-package function with a single known declared return type, and the fields of known same-package structs (`s.cache` to its field type), so that most Kubernetes calls through locals and fields get a receiver type.
40. As the developer, I want embedded structs and unknown types to stay unknown, never guessed, so that precision is not bought with invented types.
41. As the developer, I want shadowed identifiers handled by scope, so that an inner variable never inherits an outer variable's type.
42. As the developer, I want calls inside function literals attributed to the enclosing declared function or method, and a test asserting it, so that tree-sitter and GT agree on callers.
43. As the developer, I want builtin calls (`len`, `cap`, `append`, `make`, `new`, `panic`, `recover`, `copy`, `delete`, `close`, `print`, `println`, `min`, `max`, `clear`, `complex`, `real`, `imag`) not stored unless shadowed by a local symbol, and type conversions not stored, both counted as `skipped_builtin` and `skipped_conversion`, so that edges only describe real calls.
44. As the developer, I want a call kind decided by the final rules (import alias qualifier is `func`; known concrete receiver is `concrete_method`; known interface receiver is `interface_method`; call on a local identifier typed as a function is `func_value`; otherwise `unknown`), so that scoring by kind is possible.

Resolution

45. As the developer, I want a same-package function call resolved by name to `exact` (Rule 1), so that the easy case is certain.
46. As the developer, I want `alias.Name` resolved through the import map to a symbol in that package as `exact` (Rule 2), so that cross-package function calls resolve.
47. As the developer, I want a method call with a known concrete receiver type resolved to the pointer or value method as `exact` (Rule 3), so that typed method calls resolve.
48. As the developer, I want a known interface receiver resolved to every concrete type that declares every method name of the interface, as one `ambiguous` row per candidate (Rule 4, D-075), so that interface dispatch is represented without type-checking.
49. As the developer, I want same-package embedded interfaces expanded recursively with a cycle guard and cross-package or unresolvable embedded interfaces to contribute only the names known (a superset) and be counted as `interfaces_partial`, so that partial knowledge is visible.
50. As the developer, I want an interface with no known methods to produce no Rule 4 rows, so that an empty set never matches every type.
51. As the developer, I want candidates counted over the full indexed candidate set before any slice filtering, so that the cap does not depend on the slice.
52. As the developer, I want an interface call with more candidates than the cap to be stored as one `unresolved` row with reason `candidates_capped`, no callee and the raw text kept, so that Kubernetes' very common method names cannot flood the table.
53. As the developer, I want the cap chosen by a fixed rule before I see the data (smallest of 5, 10, 20, 50, 100 such that at least 95% of interface call sites have at most that many candidates; stop and escalate if that is above 50), so that precision and recall never steer the cap.
54. As the developer, I want a dry-run `--uncapped-histogram` mode that uses the same code path as the real run with a writer that writes nothing, and its output (p50, p90, p99, max and counts at 5, 10, 20, 50, 100) committed as a result file, so that the cap is evidence-based and the real run is reproducible.
55. As the developer, I want the cap to apply to Rule 4 only, so that other rules keep their behavior.
56. As the developer, I want an unknown receiver resolved against all symbols of that method name in the same package (Rule 5): one match is `name_only`, several are `ambiguous`, none is `unresolved` with reason `unknown_receiver`, so that a guess is labelled a guess.
57. As the developer, I want a call whose import path is not in the indexed filtered set (standard library, vendored, generated or excluded code) stored as `unresolved` with reason `external`, so that recall accounting is complete.
58. As the developer, I want a call whose package is indexed but no symbol matches stored as `unresolved` with reason `no_match`, so that every such row is a parser or resolver limitation I can read.
59. As the developer, I want a call through a function-typed local, parameter or closure variable stored as `unresolved` with reason `func_value`, so that deliberate non-resolution does not swamp the limitation bucket.
60. As the developer, I want promoted methods from embedded structs documented as unseen, so that the limitation is stated in the glossary and not discovered later.
61. As the developer, I want the resolution rules applied in a fixed documented order, so that the same call always gets the same row.

The graph command

62. As the developer, I want `indexer graph` to process all indexed files and not only the slice, so that the structural retriever can serve the whole repository.
63. As the developer, I want each file processed in one transaction (delete that file's tree-sitter edges, insert the new ones, record the file's freshness markers), so that an interrupted run leaves no partial file.
64. As the developer, I want the symbol index loaded once into memory, so that the run is fast.
65. As the developer, I want `--limit`, `--stats`, `--status` and `--uncapped-histogram`, and a `--package` option for the tracer, so that I can run, inspect and size the graph safely.
66. As the developer, I want SIGINT to be safe: an interrupted run followed by a rerun gives the same tree-sitter edge count as an uninterrupted run, so that I can pause it.
67. As the developer, I want a second run to print `0 files to graph`, so that idempotency is visible.
68. As the developer, I want a file to be stale when its content hash differs from the one it was graphed at or when the stored symbol-set fingerprint differs from the current fingerprint, so that a change anywhere that could change resolution is caught (D-076).
69. As the developer, I want the fingerprint to be a hash over id, kind, package, qualified name and receiver type of all indexed symbols ordered by id plus a resolver version constant, and to exclude line numbers and body hashes, so that body-only edits do not invalidate the graph.
70. As the developer, I want each change to extraction or resolution output to bump the resolver version and a test that the fingerprint changes with it, so that old edges are never silently reused.
71. As the developer, I want `parse` to leave graph state untouched, so that the stages stay independent.
72. As the developer, I want `graph --status` to list stale files and say why, so that I can see what a rerun will do.
73. As the developer, I want removing a callee and re-parsing to cause no constraint violation, the edge to vanish and the caller's file to be reported stale; a new symbol that makes a `no_match` resolvable to make all files stale; and a body-only edit to make nothing newly stale, so that all three staleness cases are tested.
74. As the developer, I want `--stats` to print edge counts per resolution and reason (summing to the stored total), per call kind, percent ambiguous, count of unresolved rows, `skipped_builtin`, `skipped_conversion`, `interfaces_partial`, the candidate-count histogram when requested, and any `deferred_*` counter, so that every call site is accounted for.
75. As the developer, I want the invariant "total call sites = stored edges + skip counters + deferred counters" asserted in the stats output, so that a call site can never vanish.
76. As the developer, I want interim `deferred_selector`, `deferred_local` and `deferred_interface` counters while the work is incomplete, each removed by the ticket that handles it, so that unfinished kinds are never counted as `no_match`.
77. As the developer, I want a statement in the first tracer's Results if local, parameter or closure identifier calls cannot yet be told apart from unresolved identifiers, so that the interim counters are interpreted correctly.
78. As the developer, I want the graph for the whole filtered repo to be run once at the end of the freshness work with its wall time recorded, so that performance is known.

Report

79. As the developer, I want `eval callgraph` to compare tree-sitter and GT edges as `(caller, callee)` symbol pairs on the slice, so that the comparison is on identity, not on line numbers.
80. As the developer, I want every stored tree-sitter edge from an in-slice caller assigned to exactly one bucket (`scored_in_slice`, `resolved_out_of_slice`, or `unresolved` by reason) and the report to assert the buckets sum to the total, with a pointer to `graph --stats` for the skip counters (which are not stored), so that no stored edge is unaccounted for (D-074).
81. As the developer, I want resolved out-of-slice edges to be neither true nor false positives and their share reported with the numbers, so that readers know the scores are in-slice only.
82. As the developer, I want the GT side to have a partition too (mapped, dropped, `gt_unmapped`) that sums to its total, so that GT loss is also accounted for.
83. As the developer, I want an ambiguous call site to count as a true positive only if the GT callee is among its candidates, and recall to count a GT pair as found if any tree-sitter edge, including an ambiguous candidate, covers it, so that ambiguity is rewarded only when correct and is reported separately.
84. As the developer, I want the ambiguous rate and mean candidate-set size reported per kind, so that I can see how much of the precision is ambiguity.
85. As the developer, I want precision and recall broken down by call kind (`func`, `concrete_method`, `interface_method`, `func_value`) and also overall, written as JSON with `precision`, `recall`, `by_kind` and `counts`, and a markdown table printed, so that results are recorded and readable.
86. As the developer, I want same-line duplicates collapsed identically on both sides, a multi-line call and a same-line duplicate to score identically on both sides, and line-level comparison to be at most secondary, so that SSA and tree-sitter line conventions cannot distort the score.
87. As the developer, I want the report to refuse to run while any file is stale according to `graph --status`, so that an incomplete or outdated graph is never scored (the last deferred code path is deleted before the report exists, so a fresh graph cannot contain deferred rows).
88. As the developer, I want all scoring queries to filter by source, so that GT rows never count as tree-sitter edges.

Error analysis and sign-off

89. As the developer, I want a seeded (seed 11) sample of 30 false positives and 30 false negatives, stratified by kind, classified by cause (unknown receiver, embedded struct, generic, closure, build tag, dynamic, and any other cause found), so that I understand where tree-sitter loses.
90. As the developer, I want recall reported per stratum (interface method, function value, plain, external), so that the weak spots are quantified.
91. As the developer, I want a fix attempted only if a cause accounts for more than 20% of errors, the change is at most 150 lines and touches one area, and before and after reports kept, so that error analysis cannot turn into open-ended tuning.
92. As the developer, I want any fix to become a new conditional ticket with a golden test, so that fixes are reviewable on their own.
93. As the developer, I want limitations that are not fixed recorded honestly in the sprint Results, so that the README can state them.
94. As the owner, I want the sign-off line added by me after the analysis and draft Results are done, as in S1, so that I approve S2 myself.
95. As the developer, I want headline numbers copied into the sprint Results with edge counts by source, resolution and kind, wall times and GT peak memory, so that the measurements the plan promised are all recorded.
96. As the developer, I want every ticket to set its own `Status` to `done` in its own commit, one commit per ticket on `main`, so that the tracker never lags.

Process and constraints

97. As the developer, I want every ticket to keep `make test` green with a throwaway database per test, to be one reviewable diff and to name its acceptance command, so that each slice is verifiable on its own.
98. As the developer, I want each unresolved reason to appear in at least one ticket's tests, so that every value of the vocabulary is exercised.
99. As the developer, I want no agent to push, open pull requests or use remote services, so that the working rules (D-069) hold.
100. As the developer, I want the decision log and glossary updated by the ticket that produces each value (D-025 and Q-04/Q-05 by the memory ticket, the cap by the histogram ticket) and later tickets only to cite them, so that each value sits next to its evidence.
101. As the developer, I want an in-memory index of package-level declared names (types, functions, variables, constants) built from the files being graphed and extended to every indexed package, so that a call to a declared named type (including types that are not stored as symbols, such as `type Foo int`) is counted as a conversion and never as `no_match`.
102. As the developer, I want local scope tracked per function (parameters, `:=`, `var`, closure variables), so that an identifier call resolves in the order local scope, same-package symbol, builtin or conversion, then `no_match`; before function values are handled, a locally declared identifier call is counted `deferred_local`, and a package-level function variable is stored `no_match` until the function-value work reclassifies it as `func_value`.

## Implementation Decisions

**Vocabulary (D-073).** `edges.resolution` has four values; `unresolved_reason` has five (external, no_match, unknown_receiver, func_value, candidates_capped). The func_value reason was added by this spec's grilling and amends D-073 (already recorded). Shape invariants are database CHECKs. `external` means the callee's import path has no row in the indexed filtered set (standard library, vendored, generated, excluded). `no_match` means the package is indexed but no symbol matched. `name_only` is a guess and always has a callee. Builtins and conversions are never stored; they are counted.

**Edge shape.** New columns: call line, call kind, unresolved reason. Unique index over (source, caller, raw callee, call line, callee-or-zero), inserts ignore conflicts. Callee reference becomes cascade on delete. Freshness columns on files: the content hash the file was graphed at, and the symbol-set fingerprint it was graphed against. One reversible migration (00005) holds all of this; the later sprint migrations are renumbered in the plan (S3 to 00006, S5 to 00007, S9 to 00008; already applied to the docs).

**Freshness (D-076).** Fingerprint = hash over (id, kind, package, qualified name, receiver type) of all indexed symbols ordered by id, plus a resolver version constant. Line numbers and body hashes are excluded. A file is stale iff its graphed hash differs from its hash or its stored fingerprint differs from the current one. `parse` does not touch graph state. Every change to extraction or resolution output bumps the resolver version.

**Extraction.** Tree-sitter re-parse (D-019). Per file: import alias map (dot and blank ignored). Per function: type environment from receiver, parameters, `var x T`, composite literals and their address, same-package constructor calls with one known return type, and fields of known same-package structs. Embedded structs and unknown types stay unknown. Calls in function literals belong to the enclosing declared function. Identifier resolution order: local scope, same-package symbol, builtin or conversion (decided with the declared-name index: package-level declared names, extended to all indexed packages), then `no_match`. Calls are classified into stored, skipped (builtin, conversion) or deferred.

**Resolution.** Five rules in fixed order: (1) same-package function by name, exact; (2) `alias.Name` through the import map, exact; (3) known concrete receiver, exact; (4) known interface receiver, one ambiguous row per candidate, candidates being concrete indexed types declaring every method name of the interface (value or pointer receivers, interfaces excluded, signatures not compared), same-package embedded interfaces expanded recursively with a cycle guard, unresolvable embedded interfaces contributing known names and counted as `interfaces_partial`, promoted methods from embedded structs unseen, zero known methods giving no rows; (5) unknown receiver against the same package only, name-only for one match, ambiguous for several. Cross-package unknown receivers are not widened (they would not fix chained calls anyway). Function-typed locals are unresolved with reason `func_value`. Candidates are counted over the whole indexed set before slice filtering.

**Cap (D-075).** Rule 4 stores one `unresolved` row (`candidates_capped`) when the candidate count exceeds the cap. The cap N is the smallest of 5, 10, 20, 50, 100 such that at least 95% of interface call sites have at most N candidates; above 50 the work stops and the ticket becomes `ready-for-human`. N is recorded in the decision log with the histogram file name. The histogram comes from a dry run with a no-op writer through the same code path. Until the cap ticket lands, the production path stores no Rule 4 rows and counts them as `deferred_interface`.

**Graph command.** All indexed files, one transaction per file, symbol index loaded once, `--limit`, `--stats`, `--status`, `--uncapped-histogram`, and a `--package` option for the tracer. Interrupt-safe and idempotent. Stats always reconcile (stored plus skipped plus deferred equals total call sites; the stored total equals the sum over resolution and reason).

**Ground truth (D-011, D-025, D-058, D-067, D-071, D-077).** `callgraph-gt` loads with the fixed build context, workspace load, `GOTOOLCHAIN=local`, name/files/syntax/types/types-info/imports/deps loading, builds SSA with generics instantiated, and computes VTA seeded by the CHA graph over only the slice functions. Edges are kept only when caller and callee are both in slice files. Mapping: qualified name, pointer receivers preserved, closures to the enclosing function, synthetic and unmappable callers dropped and counted, generic instantiations to origin, remaining failures counted as `gt_unmapped`. Kinds from SSA call mode. Rows: `source='gopackages'`, `resolution='exact'`, no reason, raw text the callee's qualified name, call line (0 if unavailable). All GT rows replaced in one transaction. The `--check` file-set equality mode is kept and must still pass.

**Memory measurement (Q-04).** A read-only run, with other jobs stopped, `GOMEMLIMIT` set and peak RSS measured and recorded in the result metadata together with packages loaded, SSA function count, VTA edge total and in-slice edge count. Above 16 GiB: write and commit an escalation report, mark that ticket `ready-for-human`, do not start the GT-storage ticket (its text says "blocked by memory ticket resolved"). No fallback is adopted by an agent. If one is adopted by the owner it needs a new decision superseding D-011's scope. Per-root runs change the type-flow set, not only cross-root edges, so they are not pre-approved.

**Isolation of ground truth (D-077).** Every retriever, tool and stats query filters on the tree-sitter source; only evaluation reads GT rows.

**Scoring (D-074, eval-protocol).** Universe: caller and callee both in slice. Identity: `(caller, callee)` symbol pair; call line is only for the unique index. Partition: every stored tree-sitter edge from an in-slice caller is in exactly one bucket (scored in slice, resolved out of slice, unresolved by reason); the report asserts the sum and points to `graph --stats` for the skip counters. GT side has its own partition. The report refuses to run while any file is stale. Out-of-slice resolved edges are neither TP nor FP. Ambiguous call sites count as true positives only if the GT callee is among the candidates; recall counts a GT pair as found if any tree-sitter edge or ambiguous candidate covers it. Breakdown by call kind; ambiguous rate and mean candidate-set size per kind.

**Error analysis.** Sample of 30 false positives and 30 false negatives, seed 11, stratified by kind, classified by cause; recall per stratum; fix only under the more-than-20%-and-at-most-150-lines rule, with before and after reports; a triggered fix is a separate conditional ticket.

**Process.** Work directly on `main`, one commit per ticket, never push (D-069). Decision-log values are recorded by the ticket that produces them: the memory ticket records D-025's invocation and the peak memory and closes Q-04 and Q-05 (deleting those entries in the same commit only if the evidence supports it); the histogram ticket records N; the last ticket only cites. The pre-sprint housekeeping (pruning Q-01 and Q-03, D-058/D-025 wording, exit criteria) is already committed.

**Ticket plan (order is binding; run sequentially; numbers are dependency order).** 01 VTA memory measurement and load invocation; 02 tracer (migration 00005 in full, plain same-package function calls for one package, declared-name index, local-scope tracking, `graph --stats` with its invariant); 03 freshness and whole-repo trial run; 04 import aliases, externals and cross-package conversions; 05 known receivers (methods, parameters, Rule 3); 06 locals, fields, constructors and unknown receivers (Rule 5); 07 Rule 4 candidates and the cap histogram; 08 apply the cap; 09 function values and the final whole-repo run; 10 GT stored with source isolation; 11 `eval callgraph`; 12 error analysis and draft Results; 13 conditional fix. Counters: `deferred_selector` is removed by 04 (alias-qualified) and 06 (the rest), `deferred_interface` by 08, `deferred_local` by 09. Every ticket from 04 to 09 bumps the resolver version. The owner's sign-off line and the ROADMAP flip to done come after ticket 12, as in S1.

Tracer package rule: rank in-slice packages by function-plus-method count, ties by import path ascending; dry-run the extractor (no writes) from the lowest and take the first with at least five same-package plain-function call sites resolving `exact` and at least one skipped builtin; name it by import path and put the ranked table in Results.

## Testing Decisions

- **What makes a good test here.** Assert external behavior only: rows and columns in a real database, command output and exit codes, the shape of result JSON. No assertions on private helpers. Golden files are never rewritten by `make test` or CI; regeneration is a manual flag. Every database test uses its own throwaway database (D-066) and fails instead of skipping.
- **Seam 1 - `indexer graph` over a committed module fixture and real Postgres.** The fixture is parsed into symbols and graphed. Asserts the exact edge rows (resolution, reason, kind, callee, line) for each rule, the three-case staleness tests, idempotent and interrupted-then-rerun counts, `--stats` output (the partition, skip and deferred counters, `interfaces_partial`), the cap (N+1 candidates gives one capped row, N gives N rows), the histogram dry run (zero writes, total equal to the stored count of an uncapped run), the CHECK shape (rows violating it are zero), cascade on callee removal, the fingerprint change with a resolver version bump, builtin and conversion skips (`len(x)` and `string(b)` skipped, `klog.V(2).InfoS` external, a missed symbol in an indexed package `no_match`), calls in function literals attributed to the enclosing function, and the source-isolation test (one GT and one tree-sitter row for the same pair leave `graph --stats` and `graph --status` unchanged). Each of the five reasons appears in at least one test.
- **Seam 2 - `callgraph-gt` over a tiny committed module fixture.** Asserts all four kinds, closure attribution, dropped synthetic callers counted, `gt_unmapped` printed, replace-in-one-transaction and identical counts on two runs, and `--check` still printing `file-set: equal`. The real-tree memory run (peak resident memory, counts) is recorded evidence, not a test.
- **Seam 3 - `eval callgraph` over hand-built edge rows.** Asserts perfect, all-ambiguous and missing cases, the partition (including out-of-slice share and unresolved by reason) summing to the total on both sides, refusal when a file is stale, multi-line and same-line-duplicate calls scoring identically on both sides, the JSON fields, and a golden of the markdown table.
- **Recorded evidence, not automated tests.** Real-corpus steps (memory measurement, the whole-repo `graph` run, the histogram, the report on the real slice, the error analysis) record the command, trimmed output and counts in the sprint Results. A real-package `no_match` count of zero is reported as zero.
- **Shared infrastructure.** No new raw skips; the no-skip rule of `make test`, `ci-local` and CI stays.
- **Prior art.** S1's parse goldens with real Postgres, the throwaway-database helper and its tests, the existing `--check` mode of `callgraph-gt`, and S1's recorded-evidence pattern.

## Out of Scope

- Using the graph in the server, retrievers or tools (S5/S6); S2 only stores it. GT is evaluation-only and never read by the server.
- Embeddings, chunks and headers (S3), summaries of any level, and any model use (S2 does no summarization).
- Resolving chained calls through return types (`s.client.CoreV1().Pods(...)`), cross-package unknown receivers, embedded-struct promotion and signature-based interface satisfaction.
- Changing the slice, the tag or the freeze (D-005, D-070); client-go is a dependency to load, not a slice root.
- Adopting CHA, RTA or per-root VTA, or any other fallback ground truth, unless the owner decides it and a new decision supersedes D-011's scope.
- General evaluation harness work and query mining (S4).
- Line-level edge comparison as a primary metric.
- Perfect resolution: error analysis may fix at most what the fix rule allows.
- Pushing, pull requests, remote CI or `gh` (D-069).
- Any change to S0 or S1 deliverables except the vocabulary and invocation amendments already committed.

## Further Notes

Traceability to the grilling (so nothing is lost): Q1 to D-073; Q2 and Q5 to D-074; Q3, Q10 and Q11 to D-075; Q4 stays in this spec (Rule 5, same-package only); Q6 to the memory measurement decision; Q7 to D-076; Q8 to D-077; Q9 to D-073 amendments (external, no_match, skips); the func_value reason is a later amendment to D-073; Q13 to the ticket plan; Q14 to the interim counters, resolver-version rule, GT mapping rules and ordering; Q15 to the tracer package rule; Q16 to the status rules; Q17 to where decisions are recorded. Round 4 confirmations: the D-025 and D-058 wording and the exit criteria are committed.

Known interpretation points to confirm at review: (a) per-kind precision is attributed by the tree-sitter edge's kind and per-kind recall by the GT edge's kind, with a separate `unknown` bucket for tree-sitter edges that could not be classified; (b) overall precision and recall are computed over the same scored universe.

Open measurements that remain open until their ticket: the GT peak resident memory (Q-04), the working load invocation and offline loadability (D-025, Q-05) and the cap N (D-075).

Wall times of the graph run and GT run, edge counts by source, resolution and kind, and GT peak memory are recorded in the sprint Results.
