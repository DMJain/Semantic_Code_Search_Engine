# 06: Locals, fields, constructors and unknown receivers (Rule 5)

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 39, 40, 41, 56, 60, 61, 98, 97, 99.

**What to build:** Most Kubernetes calls go through locals and fields (`s.cache.AssumePod`), so the type environment is extended with `var x T`, `x := T{...}`, `x := &T{...}`, `x := NewT(...)` where `NewT` is a same-package function with a single known declared return type, and the fields of known same-package structs (`s.cache` has the field's type). Embedded structs stay unknown (promoted methods are documented as unseen in the glossary). Rule 5: a selector call with a still-unknown receiver is looked up among all methods with that name in the same package: one match is `name_only`, several are `ambiguous` (one row per candidate), none is `unresolved` with reason `unknown_receiver`. Cross-package widening is not done. This removes `deferred_selector` entirely. Bump the resolver version.

**Blocked by:** 05

**Status:** done

- [x] Fixture goldens: `var x T`, `x := T{}`, `x := &T{}`, `x := NewT()` (single known return type), a constructor with two return values (stays unknown), struct field chain (`s.cache.Assume`), embedded struct (unknown), shadowed local, a method chain `a.B().C()` (unknown receiver)
- [x] Rule 5 goldens: one same-package match (name_only, has a callee), two matches (ambiguous, two rows with identical raw callee), none (unresolved/unknown_receiver)
- [x] `deferred_selector` is gone from `--stats` and the code; the invariant passes; resolver version bumped
- [x] Glossary states that promoted methods from embedded structs are unseen
- [x] Real run on two packages: counts by resolution and reason recorded
- [x] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [x] One commit `S2-06: ...` that also sets this ticket's Status to done
- [x] Report: the commands run (trimmed output) and the commit hash

Out of scope: Rule 4, the cap, function values.

## Report

Files changed: `internal/graph/extract.go` (`Decl.ConstructorLocals`,
`Call.Fields`, `callShape` selector-chain walk, `addInferredLocals`/`inferLocals`
local type inference, `TypeRef`, `FileFacts`/`SourceFacts`, `collectStructFields`,
`collectFuncResults`, `typeRefFromWritten`, `singleResult`), `internal/graph/resolve.go`
(`Index.methodsByPkgName`/`MethodsByName`, `NameIndex` fields and results as
`TypeRef` plus their digest sections, `analyzeSelector`/`analyzeAlias`/
`analyzeUnknownReceiver`/`resolveChain`/`baseType`/`resolveWrittenType`, Rule 3
field chains and constructor locals), `internal/graph/
stats.go` (remove `DeferredSelector`, document the ambiguous call-unit
expansion), `internal/graph/version.go` (3 -> 4 and the package doc),
`cmd/indexer/graph.go` (`buildNameIndex` uses `SourceFacts`, `--stats` drops
`deferred_selector`), tests `internal/graph/{extract,resolve}_test.go`,
`cmd/indexer/graph_test.go`, fixtures `cmd/indexer/testdata/graph/locals/locals.go`
and `.../rule5/rule5.go`, and docs (glossary, S2 sprint Results, this ticket).

Type inference and field chains. `extractDecl` keeps the receiver/parameter type
environment and adds `addInferredLocals`: `inferLocals` counts every local
declaration (short var, var, range, receive, type-switch guard, function-literal
parameter, const) so a redeclaration makes a name unknown, and records a written
type for `var x T`, `var x = T{...}`, `x := T{...}` and `x := &T{...}`, or the
constructor name for `x := NewT(...)` (a local shadowing the constructor name is
not treated as the package function). At Analyze time a constructor name is
resolved to the function's single declared result type through `NameIndex`
(populated by `SourceFacts`, which also records named struct fields and skips
embedded ones; results are counted by NAME, so a grouped named result `(a, b T)`
is two results and not a constructor). Struct field types and constructor
results are stored as a `TypeRef{Pkg, Name}` resolved against the DECLARING
file's aliases at extraction time, so a caller file binding the same alias to a
different package cannot redirect a field chain or constructor to a wrong
`exact`. Field chains are resolved within one package by `resolveChain`: base
type, then each intermediate field's `TypeRef` through the struct field map,
then the final method via Rule 3; a chain that leaves the package, or hits an
unknown/embedded field, is unknown.

Rule 5. `deferred_selector` is deleted; the Analyze dispatch is identifier,
selector (Rule 3 when a known type, Rule 2 when an import alias, else Rule 5),
and unknown receiver. `analyzeUnknownReceiver` looks up `MethodsByName(pkg,
field)`: one -> `name_only` with that callee, several -> one `ambiguous` row per
candidate with identical raw text, none -> `unresolved` with reason
`unknown_receiver` and a NULL callee. Rows carry kind `unknown`. An ambiguous
site adds one call unit per candidate to `Stats.CallSites`, so the invariant
`call sites = stored + skipped + deferred` still holds with multiple rows per
site. `deferred_local` and `deferred_interface` are unchanged.

Fixture goldens. New `locals` package (`TestGraphStageLocalsGoldens`): `var x T`,
`var x = T{}`, `x := T{}`, `x := &T{}` and single-result `NewT()` -> Rule 3
exact; two-result `NewT2()` -> Rule 5 name_only; field chain `s.cache.Assume` ->
Rule 3 exact `(*Cache).Assume`; embedded `Outer.Promoted` -> Rule 5 name_only;
shadowed local -> Rule 5 name_only; `a.B().C()` -> inner Rule 3 exact, outer
Rule 5 name_only; `invariant: call sites 13 = stored 13 + skipped 0 + deferred 0
[ok]`, exact 9, name_only 4. New `rule5` package
(`TestGraphStageRule5Goldens`): one match `y.Only` -> name_only with callee
`(A).Only`; two matches `y.Two` -> two ambiguous rows with identical raw text;
none `y.Missing` -> unresolved/unknown_receiver; `invariant: call sites 4 =
stored 4 + skipped 0 + deferred 0 [ok]`. Existing goldens updated: plain
`a.Method` -> unknown_receiver; alias blank-import and the two chained
`klog.V(2).InfoS` (name_only `fakeKlog.InfoS`); methods Rule 5 ambiguous/name_only
(`invariant: call sites 22 = stored 21 + skipped 0 + deferred 1`). Unit tests
`TestExtractLocalTypes`, `TestExtractShadowedLocalStaysUnknown`, `TestSourceFacts`
(grouped named results are not constructors), `TestSourceFactsResolvesDeclaringAlias`,
`TestAnalyzeConstructorAndFieldChain`, `TestAnalyzeRule5`,
`TestAnalyzeFieldTypeUsesDeclaringAlias` (a colliding field type resolves to the
declaring package, never the caller's other package),
`TestAnalyzeInterfaceFieldFallsToDeferred` and
`TestAnalyzeConstructorShadowedByLocal` pin the same logic; the fingerprint test
now also changes a struct field type and a constructor result.

Real runs (dev DB, `--package`; the rest of the tree stays stale until ticket 09):

```
$ go run ./cmd/indexer graph --package k8s.io/kubernetes/pkg/scheduler --stats
graphed 5 files, stored 1287 edges
  resolution exact: 464 | name_only: 29 | ambiguous: 0 | unresolved: 794
  reason external: 236 | no_match: 73 | unknown_receiver: 485
call kind func: 282 | concrete_method: 182 | unknown: 823
deferred_local: 25 | deferred_interface: 137
invariant: call sites 1612 = stored 1287 + skipped 163 + deferred 162 [ok]

$ go run ./cmd/indexer graph --package k8s.io/kubernetes/pkg/scheduler/backend/cache --stats
graphed 5 files, stored 649 edges
  resolution exact: 220 | name_only: 23 | ambiguous: 120 | unresolved: 286
  reason external: 92 | no_match: 6 | unknown_receiver: 188
call kind func: 149 | concrete_method: 71 | unknown: 429
deferred_interface: 3
invariant: call sites 835 = stored 649 + skipped 183 + deferred 3 [ok]
```

`deferred_selector` is absent from both outputs. DB check: 0 rows break the
unresolved shape rule, 120 `ambiguous` rows, 0 `name_only` rows with a NULL
callee. Before the review fix `pkg/scheduler` was exact 440, name_only 30,
unknown_receiver 582, deferred_interface 63; resolving field types against the
declaring file's aliases moved interface fields such as `Scheduler.SchedulingQueue`
into deferred_interface (63 -> 137) and legitimate field methods into exact
(440 -> 464), cutting unknown_receiver (582 -> 485).

Review fixes. (1) `singleResult` counts result names, so `func F() (a, b T)` is
not a constructor. (2) D-076 is amended (2026-10-14) to include the struct field
types and constructor return types in the fingerprint, with digest tests. (3)
Qualified field and constructor result types are resolved at extraction time
against the declaring file's aliases and stored as an import path, never a wrong
`exact`; caller-alias collisions are covered by
`TestAnalyzeFieldTypeUsesDeclaringAlias`. Plus minors: the constructor-name
shadowing check, the `const` declaration count in `inferLocals`, and the glossary
now states embedded promotion is unseen for Rule 3 as well as Rules 4 and 5.

Checks. `make test` green (throwaway DB per test, no skips); `make ci-local`
green. No git command was run; the commit hash is left to the orchestrator's
post-review commit.

Ambiguity. Rule 5 is a name-only guess that ignores the receiver type: a chained
`klog.V(2).InfoS` matches the unrelated same-package `fakeKlog.InfoS`, and a
receiver whose type is known but lacks the method (embedded promotion, a
`Foo int` selector) also reaches Rule 5 rather than a deferred counter. A
two-result (or grouped named-result) constructor's local stays unknown, not Rule
3. Field chains and Rule 5 do not cross package boundaries (a qualified field
type is resolved to its declaring package path, but the chain does not step into
another package's fields); a constructor is a package-level function with exactly
one declared result (results counted by name; methods are not constructors).
