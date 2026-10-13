# 04: Import aliases, externals and cross-package conversions

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 38, 43, 46, 57, 58, 61, 98, 101, 97, 99.

**What to build:** Calls written as `alias.Name` resolve to the right place. Each file gets an import alias map (alias to import path); dot and blank imports are ignored; a local variable that shadows an alias hides it. Rule 2: `alias.Name` where the package is indexed and has a function named `Name` is `exact`, kind `func`. If the import path is not an indexed package (standard library, vendored, generated, excluded) the call is stored `unresolved` with reason `external`. If the package is indexed but has no matching function the call is `no_match`, unless `Name` is a declared type there, in which case it is a conversion and is counted `skipped_conversion`. For that, the declared-name index from ticket 02 is extended to every indexed package. The mapping from staging directories to the `k8s.io/client-go/...` import path is handled in the package lookup.

This removes the alias-qualified part of `deferred_selector`; receiver-style selector calls stay deferred. Bump the resolver version.

**Blocked by:** 03

**Status:** done

- [ ] Fixture goldens: aliased import (exact, func), the same name in two packages (collision resolves by alias), dot and blank imports ignored, shadowed alias, `klog.V(2).InfoS` (external), a missed symbol in an indexed package (no_match), `alias.Type(x)` conversion to a non-struct type (skipped_conversion), a staging-style import path
- [ ] `deferred_selector` no longer counts alias-qualified calls; receiver-style selector calls are still counted there
- [ ] The resolver version is bumped and the staleness check reports all files stale afterwards
- [ ] Real run on the tracer package and one package that imports client-go: `--stats` invariant passes; external, exact and no_match counts recorded
- [ ] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [ ] One commit `S2-04: ...` that also sets this ticket's Status to done
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Receiver, parameter, local or field types; Rules 3 to 5; the final whole-repo run.

## Report

Files changed: `internal/graph/extract.go` (per-file import alias map incl. the
versioned-path default alias, the alias-qualified call shape, `DeclaredNames`),
`internal/graph/resolve.go` (cross-package `NameIndex`, Rule 2 in
`analyzeQualified`, fingerprint over symbols + declared names), `internal/graph/version.go`
(`ResolverVersion` 1 -> 2 and the package doc), `cmd/indexer/graph.go`
(`buildNameIndex`, fingerprint built after it), plus tests
`internal/graph/extract_test.go`, `internal/graph/resolve_test.go`,
`cmd/indexer/graph_test.go`, fixtures under `cmd/indexer/testdata/graph/`
(`alias`, `depone`, `deptwo`, `dotpkg`, `blankpkg`, `typed`,
`staging/src/k8s.io/client-go/tools/cache`), and docs
(`docs/plan/decisions.md` D-076 amendment, `docs/plan/glossary.md`,
`docs/plan/architecture.md`, this ticket, and the S2 sprint Results).

Classification. A selector call whose operand is a bare identifier carries
`Qualifier`/`Field`; every other selector (receiver chain, chained call) stays a
deferred selector. The default alias of an unaliased import is the last path
segment, except a major-version module suffix `/vN` (N >= 2), whose package name is
the parent segment (`k8s.io/klog/v2` -> `klog`; `.../v1`, `v1beta1`, `v1alpha1`
stays). In `analyzeQualified`: a shadowing local or an unknown qualifier is
`deferred_selector`; a same-package function in the aliased package is `exact`
kind `func`; an import path not in the indexed-package set is
`unresolved`/`external`; an indexed package without the function is
`skipped_conversion` for a declared `type`, else `unresolved`/`no_match`. Dot and
blank imports are dropped from the alias map; `deferred_selector` no longer counts
alias-qualified calls. Fixture goldens cover the aliased exact (two packages with
the same name resolving by alias), dot and blank imports ignored, a shadowed
alias, an UNALIASED versioned import (`"k8s.io/klog/v2"`, inner `klog.V` external),
an indexed-package miss (`no_match`), a non-struct conversion
(`skipped_conversion`), and a staging-style import path. `TestDefaultAlias` pins
the derivation cases.

Fingerprint (D-076 amendment). The fingerprint now hashes the indexed symbols
`(id, kind, package, qualified_name, receiver)` ordered by id AND the declared-name
index `(package, name, kind)` ordered deterministically, plus the resolver version,
so a non-symbol declaration (`type Foo int`) that changes another file's
classification marks it stale. Both the staleness check and the stored
`graphed_symbols_fp` use the same digest; `--status` now builds the index too.
`TestGraphStalenessDeclaredNameChange` covers it.

Cross-package declared-name index. `graph.DeclaredNames` is a names-only
tree-sitter pass; `buildNameIndex` scans every indexed file from `files`
(`SELECT path, package`) once per run and merges into `NameIndex`, which also
holds the indexed-package set. `files.package` is already the Go import path
(`walk.ImportPath`), so the staging mapping is the existing lookup and no extra
mapping was added. Whole-repo cost: one extra parse pass over all 6633 indexed
files; the real `--package` runs took ~4.4-4.8 s wall each including it.

Real run (dev DB). After the bump `graph --status` reports `6633 files stale`
(`symbols changed: 6633`). `--package k8s.io/kubernetes/pkg/kubelet/envvars`:
exact 6, external 14, no_match 0, deferred_selector 0, `call sites 32 = stored 20
+ skipped 12 + deferred 0 [ok]`. `--package k8s.io/kubernetes/pkg/scheduler`:
exact 282, external 236, no_match 0, skipped_conversion 4, deferred_selector 906,
deferred_local 25, `call sites 1612 = stored 518 + skipped 163 + deferred 931
[ok]`. 114 of the scheduler external rows are `klog.*` (verified in the DB:
`GROUP BY unresolved_reason` -> `external|114`); before the versioned-path alias
fix they were `deferred_selector`. Trimmed output is in the sprint Results.

Checks. `CGO_ENABLED=1 go test -count=1 ./internal/graph/... ./cmd/indexer/...`
green; `make test` green (throwaway DB per test, no skips); `make ci-local` green
(`go vet`, tests, golangci-lint 0 issues, shellcheck, actionlint).

Ambiguity. The default alias is inferred from the import path only, so a package
whose declared name differs from its path's last segment (any `/vN` N >= 2, or an
unconventional name) still relies on the segment rule. Commit is left to the
orchestrator (no git run here).

