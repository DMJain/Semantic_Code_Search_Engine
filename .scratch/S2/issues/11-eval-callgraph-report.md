# 11: `eval callgraph` precision and recall report

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 79-88, 95, 98, 97, 99.

**What to build:** `go run ./cmd/eval callgraph` compares the tree-sitter graph with the ground truth on the slice and records honest numbers by call kind. The scored universe is caller and callee both in slice; identity is the `(caller, callee)` symbol pair. Every stored tree-sitter edge from an in-slice caller falls in exactly one bucket (scored in slice, resolved out of slice, unresolved by reason) and the report asserts the buckets sum to the total, with a pointer to `graph --stats` for the skip counters. The GT side has its own partition that sums to its total. Resolved out-of-slice edges are neither true nor false positives; their share is printed. An ambiguous call site is a true positive only if the GT callee is among its candidates; recall counts a GT pair as found if any tree-sitter edge (including an ambiguous candidate) covers it. Precision by tree-sitter kind, recall by GT kind (with an `unknown` bucket for tree-sitter edges of unknown kind); ambiguous rate and mean candidate-set size per kind. Same-line duplicates collapse identically on both sides. The report refuses to run while `graph --status` shows any stale file. Output is JSON (`precision`, `recall`, `by_kind` with `precision`, `recall`, `ambiguous_rate` per kind, `counts`) and a markdown table.

**Blocked by:** 09, 10

**Status:** ready-for-agent

- [ ] Tests over hand-built edge rows: perfect, all-ambiguous, missing, out-of-slice share, unresolved by reason; both partitions sum to the total; a multi-line call and a same-line duplicate score identically on both sides; refusal when a file is stale; golden for the JSON and the markdown table
- [ ] `go run ./cmd/eval callgraph` exits 0 on the real data, writes the JSON with the fields above, prints the table
- [ ] The stdout and JSON state that the numbers are in-slice only and give the out-of-slice share
- [ ] Headline numbers, edge counts by source, resolution and kind, and wall times are copied into the S2 Results section of the sprint doc
- [ ] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [ ] One commit `S2-11: ...` that also sets this ticket's Status to done
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: The general evaluation harness (S4); error analysis (ticket 12); line-level comparison as a primary metric.
