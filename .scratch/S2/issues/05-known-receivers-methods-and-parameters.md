# 05: Known receivers: methods and parameters (Rule 3)

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 39 (receiver and parameters), 44, 47, 61, 98, 97, 99.

**What to build:** Method calls whose receiver type is known from the method's own receiver or from a parameter resolve to the concrete method. The per-function type environment now holds the receiver (`s *Scheduler`) and the parameters. Rule 3: a call on a known concrete type resolves to the pointer or value method with that name, `exact`, kind `concrete_method`. A call on a known interface type gets kind `interface_method` and is counted `deferred_interface` (Rule 4 comes in tickets 07 and 08). Types that are unknown stay unknown and are never guessed; embedded structs stay unknown. Shadowing is by scope, so an inner variable never inherits an outer type. Selector calls whose receiver is still unknown (locals, fields, chains) stay `deferred_selector` until ticket 06. Bump the resolver version.

**Blocked by:** 04

**Status:** ready-for-agent

- [ ] Fixture goldens: pointer receiver, value receiver, parameter-typed call, a method with the same name on two types (resolves by receiver type), shadowed parameter, embedded struct left unknown, an interface-typed parameter (kind interface_method, counted deferred_interface)
- [ ] `--stats` shows `concrete_method` edges and the invariant passes; resolver version bumped
- [ ] Real run on one scheduler package: exact `concrete_method` count and remaining deferred counts recorded
- [ ] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [ ] One commit `S2-05: ...` that also sets this ticket's Status to done
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Locals, struct fields, constructors, Rule 5, Rule 4.
