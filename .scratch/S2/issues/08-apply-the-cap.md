# 08: Apply the cap: Rule 4 rows and candidates_capped

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 48, 50-52, 55, 98, 97, 99.

**What to build:** Interface calls are stored. `MaxInterfaceCandidates` is set to the N recorded in D-075. Up to N candidates give one `ambiguous` row per candidate (D-023), kind `interface_method`. More than N candidates give one `unresolved` row with reason `candidates_capped`, no callee and the raw callee text kept. The cap applies to Rule 4 only. This removes `deferred_interface`. Bump the resolver version.

**Blocked by:** 07

**Status:** done

- [x] Fixture tests: N implementers give N ambiguous rows with identical raw callee text; N+1 give one `candidates_capped` row with a NULL callee
- [x] The constant equals the N in D-075
- [x] `deferred_interface` is gone from `--stats` and the code; the invariant passes; resolver version bumped
- [x] Real run on one interface-heavy package: counts of ambiguous rows and capped rows recorded
- [x] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [x] One commit `S2-08: ...` that also sets this ticket's Status to done
- [x] Report: the commands run (trimmed output) and the commit hash

Out of scope: Function values; the final whole-repo run.

## Report

What changed. Production Rule 4 stores rows. `internal/graph.MaxInterfaceCandidates = 50`
(named constant, D-075), pinned by `TestMaxInterfaceCandidatesIsD075`. `Rule4`
lost its `Compute` flag: `Analyze` always resolves Rule 4, defaulting to
`N = MaxInterfaceCandidates`, and `--uncapped-histogram` uses `N = 0` (uncapped).
`cmd/indexer/graph.go` builds `Rule4{N: MaxInterfaceCandidates}` (plus a
`graphOptions.Rule4N` test override) and no longer prints `deferred_interface`.
`Stats.DeferredInterface` and every increment are removed, so `Deferred()` is
`DeferredLocal` alone and the invariant `call sites = stored + skipped + deferred`
still holds. `graph.ResolverVersion` bumped 5 -> 6.

Rule 4 wiring. A known interface stores one `ambiguous` `interface_method` row per
candidate up to N; more than N stores one `unresolved` `candidates_capped` row
with a NULL callee and the raw text kept; candidates but none declaring the
called name stores one `unresolved` `no_match` row; zero known methods stores no
Rule 4 row and falls through to Rule 5. The cap applies to Rule 4 only. The cap
decision is shared: production (`analyzeInterface`) and `Index.InterfaceCandidates`
both call `capExceeded(cands, n)`, so they cannot drift, and the internal
`Rule4.N`/`CappedCandidates` seams are kept for the ticket tests. The D-075 cap
lives once: `maxNonEscalatingCap = MaxInterfaceCandidates` in `stats.go`.

Zero-candidate choice. Interface with known methods but no indexed implementer ->
one `unresolved` `no_match` row, kind `interface_method` (keeps the D-073
invariant exact now that `deferred_interface` is gone). `interfaces_partial` is
still incremented for a partial interface.

Empty-method choice (spec story 50). Interface with no known method -> no Rule 4
row, fall through to Rule 5 (same-package method-name lookup), so an empty set
never matches every type and the call site stays accounted for; no
`interfaces_partial` increment because it is not a Rule 4 row. Interpretation
note: the fall-through row carries the Rule 5 kind `unknown`, not
`interface_method` (story 44 lists a known interface receiver as
`interface_method`). Since no Rule 4 row is produced and the row is a genuine
Rule 5 guess, `unknown` is the honest kind; this is a wording tension in story 44,
not a bug.

Fixture results (`cmd/indexer/testdata/graph/interfaces/`, real Postgres):
`callCap3` (three implementers) -> three ambiguous `interface_method` rows with
identical raw text; `callCap4` (four) -> four rows at the production cap and one
`candidates_capped` row with a NULL callee under a test cap of 3; `callNoImpl` ->
one `no_match` row; `callEmpty` -> Rule 5. Production invariant
`call sites 16 = stored 16 + skipped 0 + deferred 0 [ok]`.

Real runs (dev DB). Interface-heavy package with a low cap hit:
`go run ./cmd/indexer graph --package k8s.io/kubernetes/pkg/scheduler/backend/cache --stats`
-> 5 files, 652 edges stored, rule-4 `ambiguous` `interface_method` rows = 3,
`candidates_capped` rows = 0, `deferred_interface` absent, invariant
`call sites 835 = stored 652 + skipped 183 + deferred 0 [ok]`.

Real `candidates_capped` evidence. `k8s.io/apimachinery/pkg/runtime` and
`k8s.io/kubernetes/pkg/kubelet` were already graphed at resolver version 6, so
their graphed markers were cleared to force a fresh, independent re-graph (the
pipeline's own staleness path), then:
`go run ./cmd/indexer graph --package k8s.io/apimachinery/pkg/runtime --stats`
-> 21 files, 1255 edges stored, rule-4 `ambiguous` `interface_method` rows =
**418**, `candidates_capped` rows = **2**, `no_match` `interface_method` rows = 0,
invariant `call sites 1400 = stored 1255 + skipped 133 + deferred 12 [ok]`.
The stored rows for `k8s.io/kubernetes/pkg/kubelet` show the same shape:
`ambiguous` `interface_method` = **672**, `candidates_capped` = **2**,
`no_match` `interface_method` = 1.

Checks. `make test` green (exit 0, no skips); `make ci-local` green (`go vet`,
tests, golangci-lint 0 issues, shellcheck, actionlint).

Commit: the orchestrator commits this diff as `S2-08: Apply the cap: Rule 4 rows
and candidates_capped`.
