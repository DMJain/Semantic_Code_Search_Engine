# 06: Locals, fields, constructors and unknown receivers (Rule 5)

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 39, 40, 41, 56, 60, 61, 98, 97, 99.

**What to build:** Most Kubernetes calls go through locals and fields (`s.cache.AssumePod`), so the type environment is extended with `var x T`, `x := T{...}`, `x := &T{...}`, `x := NewT(...)` where `NewT` is a same-package function with a single known declared return type, and the fields of known same-package structs (`s.cache` has the field's type). Embedded structs stay unknown (promoted methods are documented as unseen in the glossary). Rule 5: a selector call with a still-unknown receiver is looked up among all methods with that name in the same package: one match is `name_only`, several are `ambiguous` (one row per candidate), none is `unresolved` with reason `unknown_receiver`. Cross-package widening is not done. This removes `deferred_selector` entirely. Bump the resolver version.

**Blocked by:** 05

**Status:** ready-for-agent

- [ ] Fixture goldens: `var x T`, `x := T{}`, `x := &T{}`, `x := NewT()` (single known return type), a constructor with two return values (stays unknown), struct field chain (`s.cache.Assume`), embedded struct (unknown), shadowed local, a method chain `a.B().C()` (unknown receiver)
- [ ] Rule 5 goldens: one same-package match (name_only, has a callee), two matches (ambiguous, two rows with identical raw callee), none (unresolved/unknown_receiver)
- [ ] `deferred_selector` is gone from `--stats` and the code; the invariant passes; resolver version bumped
- [ ] Glossary states that promoted methods from embedded structs are unseen
- [ ] Real run on two packages: counts by resolution and reason recorded
- [ ] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [ ] One commit `S2-06: ...` that also sets this ticket's Status to done
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Rule 4, the cap, function values.
