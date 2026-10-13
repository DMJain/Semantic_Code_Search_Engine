# 04: Import aliases, externals and cross-package conversions

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 38, 43, 46, 57, 58, 61, 98, 101, 97, 99.

**What to build:** Calls written as `alias.Name` resolve to the right place. Each file gets an import alias map (alias to import path); dot and blank imports are ignored; a local variable that shadows an alias hides it. Rule 2: `alias.Name` where the package is indexed and has a function named `Name` is `exact`, kind `func`. If the import path is not an indexed package (standard library, vendored, generated, excluded) the call is stored `unresolved` with reason `external`. If the package is indexed but has no matching function the call is `no_match`, unless `Name` is a declared type there, in which case it is a conversion and is counted `skipped_conversion`. For that, the declared-name index from ticket 02 is extended to every indexed package. The mapping from staging directories to the `k8s.io/client-go/...` import path is handled in the package lookup.

This removes the alias-qualified part of `deferred_selector`; receiver-style selector calls stay deferred. Bump the resolver version.

**Blocked by:** 03

**Status:** ready-for-agent

- [ ] Fixture goldens: aliased import (exact, func), the same name in two packages (collision resolves by alias), dot and blank imports ignored, shadowed alias, `klog.V(2).InfoS` (external), a missed symbol in an indexed package (no_match), `alias.Type(x)` conversion to a non-struct type (skipped_conversion), a staging-style import path
- [ ] `deferred_selector` no longer counts alias-qualified calls; receiver-style selector calls are still counted there
- [ ] The resolver version is bumped and the staleness check reports all files stale afterwards
- [ ] Real run on the tracer package and one package that imports client-go: `--stats` invariant passes; external, exact and no_match counts recorded
- [ ] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [ ] One commit `S2-04: ...` that also sets this ticket's Status to done
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Receiver, parameter, local or field types; Rules 3 to 5; the final whole-repo run.
