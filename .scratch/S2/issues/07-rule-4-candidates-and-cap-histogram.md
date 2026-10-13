# 07: Rule 4 candidates and the cap histogram

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 48-51, 53-55, 98, 100, 97, 99.

**What to build:** The candidate set for an interface call is computed correctly and the cap value is chosen from evidence. Candidates are concrete indexed types that declare every method name of the interface (value or pointer receivers, interfaces excluded, signatures not compared). Same-package embedded interfaces are expanded recursively with a cycle guard; a cross-package or unresolvable embedded interface contributes only the names known (a superset) and increments `interfaces_partial`; an interface with no known methods yields no candidates. Candidates are counted over the full indexed set before any slice filtering. The candidate function takes N as a parameter.

`indexer graph --stats --uncapped-histogram` runs the same code path as a real run with a writer that writes nothing and prints p50, p90, p99, max and the counts at 5, 10, 20, 50 and 100 candidates; the output is committed as a result file. N is the smallest of 5, 10, 20, 50, 100 such that at least 95% of interface call sites have at most N candidates, and is recorded in D-075 with the histogram file name. If that N is above 50, stop: set this ticket's Status to ready-for-human and write the escalation. The production path in this ticket still stores no Rule 4 rows (they stay `deferred_interface`). Bump the resolver version.

**Blocked by:** 06

**Status:** ready-for-agent

- [ ] Fixture tests: `interface{Get;Set}` with `A{Get,Set}` and `B{Get}` gives only A; same-package embedded interface expands; cross-package embedded gives `interfaces_partial=1`; an embedding cycle terminates; value and pointer receivers both match; an interface with zero known methods gives no candidates
- [ ] The dry run leaves the edge count unchanged; its total equals the stored row count of an uncapped fixture run
- [ ] The histogram result file is committed for the real tree
- [ ] D-075 records N (chosen by the stated rule, not from precision or recall) and the histogram file name, replacing PENDING; if N > 50 the ticket is ready-for-human and nothing else is required
- [ ] Production `graph` still stores no Rule 4 rows and counts them `deferred_interface`; resolver version bumped
- [ ] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [ ] One commit `S2-07: ...` that also sets this ticket's Status to done
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Storing Rule 4 rows or capped rows (ticket 08).
