# 05: Known receivers: methods and parameters (Rule 3)

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 39 (receiver and parameters), 44, 47, 61, 98, 97, 99.

**What to build:** Method calls whose receiver type is known from the method's own receiver or from a parameter resolve to the concrete method. The per-function type environment now holds the receiver (`s *Scheduler`) and the parameters. Rule 3: a call on a known concrete type resolves to the pointer or value method with that name, `exact`, kind `concrete_method`. A call on a known interface type stores no row and sets no `call_kind`; only the `deferred_interface` counter moves (the `interface_method` kind becomes observable when Rule 4 lands, in tickets 07 and 08). Types that are unknown stay unknown and are never guessed; embedded structs stay unknown; a type parameter is never a type. Shadowing is by scope, so an inner variable never inherits an outer type. Selector calls whose receiver is still unknown (locals, fields, chains) stay `deferred_selector` until ticket 06. Bump the resolver version.

**Blocked by:** 04

**Status:** done

- [x] Fixture goldens: pointer receiver, value receiver, parameter-typed call, a method with the same name on two types (resolves by receiver type), shadowed parameter, embedded struct left unknown, an interface-typed parameter (kind interface_method, counted deferred_interface)
- [x] `--stats` shows `concrete_method` edges and the invariant passes; resolver version bumped
- [x] Real run on one scheduler package: exact `concrete_method` count and remaining deferred counts recorded
- [x] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [ ] One commit `S2-05: ...` that also sets this ticket's Status to done
- [x] Report: the commands run (trimmed output) and the commit hash

Out of scope: Locals, struct fields, constructors, Rule 5, Rule 4.

## Report

Files changed: `internal/graph/extract.go` (per-declaration type environment
`Decl.Types`: receiver + parameter written types; `Decl.TypeParams`; shadowing
removal including type-switch guards and select receives),
`internal/graph/resolve.go` (`Index` method/interface maps, `ConcreteMethod`,
`IsInterface`, `analyzeReceiver`, `receiverType` with declared-name and
type-parameter gating, Rule 3 wired into `analyzeQualified` before the
local/alias branches), `internal/graph/version.go` (`ResolverVersion` 2 -> 3 and
the package doc), plus tests `internal/graph/extract_test.go`,
`internal/graph/resolve_test.go`, `cmd/indexer/graph_test.go`, the fixture
`cmd/indexer/testdata/graph/methods/methods.go`, and docs (glossary unchanged;
S2 sprint Results; this ticket).

Type environment. `Decl.Types` maps the receiver name and every parameter name to
its written type text. It is built in `extractDecl`: the receiver's
`parameter_declaration` gives its name list and `receiverText` gives the type;
each parameter's `type` field is paired with its name list (so `a, b int` gives
both names `int`, asserted in `TestExtractTypeEnvironment`); and the
declaration's own `type_parameters` names go into `Decl.TypeParams`.
`removeShadowed` then walks the body and deletes any name redeclared by a `:=`,
`var`/`const`, range clause, function-literal parameter, type-switch guard
(`switch s := y.(type)`) or select receive (`case s := <-ch:`); the same two
forms are added to the local-scope collector so such a name is `deferred_local`
for a bare identifier call rather than `no_match`. The scope is flat per
declaration, matching the tracer's flat local-scope simplification: an inner
declaration always wins, so it can only turn a known name into an unknown one,
never invent a type (it can miss a resolution but never produce a wrong exact).
Unknown written forms (slices, maps, funcs, builtins, `error`, `any`, inline
structs) are stored as written and rejected by the resolver.

Rule 3. `analyzeQualified` checks the type environment first, then local scope,
then the import alias. `receiverType` reduces the written type to a named type:
strip one leading `*` (Go auto-addresses, value and pointer bases resolve alike),
a bare identifier is a same-package type only when the package actually declares
a type of that name (the declared-name index), `alias.Type` resolves through a
known import alias only when the aliased package declares the type too, and the
declaration's own type parameters are never types; any other form is not a named
type. A same- or cross-package method with a matching receiver base is `exact`
kind `concrete_method`; a known named interface stores no row and sets no
`call_kind`, and is counted `deferred_interface` until Rule 4 lands; anything
else (no such method, embedded-only promotion, type parameter, unknown form)
stays `deferred_selector`. The method index and interface set are built once in
`NewIndex`.

Fixture goldens. `TestGraphStageMethodGoldens` (pointer receiver, value receiver,
parameter-typed call, `Box.M`/`Crate.M` resolved by receiver type, interface
parameter -> deferred_interface, embedded `Outer` -> deferred, parameter shadowed
by `:=`, type-switch guard shadowing a receiver, select receive shadowing a
receiver, and a type parameter `x T` that shares a name with a same-named package
type with that method) passes with `invariant: call sites 12 = stored 6 + skipped
0 + deferred 6 [ok]`, exact 6, concrete_method 6, deferred_selector 5,
deferred_interface 1. `TestExtractTypeEnvironment*` pins the environment
(including grouped parameters) and the shadowing removal; `TestExtractTypeParameters`
pins the type-parameter set. `TestAnalyzeKnownReceiverCrossPackage` covers a
`d *dep.T` parameter resolving to `dep.(T).M` through an import alias;
`TestAnalyzeTypeParameterNotGuessed` and `TestAnalyzeBareTypeNotDeclaredNotGuessed`
prove the gating. The existing `alias` golden's `callShadowed(klog *fakeKlog)` now
resolves the inner `klog.V` to `concrete_method` (the parameter type is known)
instead of deferring, so the alias invariant is `call sites 11 = stored 7 +
skipped 1 + deferred 3 [ok]`.

Real run. `go run ./cmd/indexer graph --package k8s.io/kubernetes/pkg/scheduler
--stats` (5 files re-graphed; the rest of the tree stays stale until ticket 09):
stored 654 = exact 418 (func 282 + concrete_method 136) + unresolved 236
(external); `call kind concrete_method: 136`; `deferred_selector: 710`,
`deferred_interface: 60`, `deferred_local: 25`; `invariant: call sites 1612 =
stored 654 + skipped 163 + deferred 795 [ok]`. The 196 receiver/parameter-typed
selector sites (deferred_selector 906 -> 710) split exactly into 136
concrete_method and 60 deferred_interface. DB check: 136
`call_kind='concrete_method'` rows for the package, 0 rows breaking the
unresolved shape rule. The review fixes leave this package unchanged: forcing the
five files stale (`graphed_symbols_fp = NULL`; the resolver version stays 3) and
re-running gives the identical counts, and a following run prints `0 files to
graph`.

Checks. `make test` green (throwaway DB per test, no skips); `make ci-local`
green (`go vet`, tests, golangci-lint 0 issues, shellcheck, actionlint). No git
command was run; the commit is left to the orchestrator.

Ambiguity. Embedded-struct promotion is deliberately unseen (the method index
holds only methods declared on the named type), so a promoted method stays
`deferred_selector`. Cross-package receivers resolve only through a known import
alias (`pkg.T`); an unaliased or dot-imported type has no alias and stays
deferred. A same-package bare type name is looked up only in the same package and
only when the declared-name index knows it as a type; a name that is the
declaration's own type parameter is never a type even if the package declares a
same-named type. Builtin interfaces (`error`, `any`) are neither declared types
nor in the interface set, so `err.Error()` is counted `deferred_selector`, not
`deferred_interface`, and stores no wrong edge; no special case is added for
them.
