# 02: Tracer: plain calls for one package, with migration 00005

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 26-34, 37, 42-45, 61-65, 74-77, 98, 101, 102, 97, 99.

**What to build:** Running `indexer graph --package <import path>` on one small real package produces correct tree-sitter edges for plain function calls and a `--stats` report in which every call site is accounted for. It is the first slice through every layer: the full migration, the fixture harness, extraction, resolution Rule 1, storage and the stats invariant.

The package is chosen by rule: rank in-slice packages by function-plus-method count (ties by import path ascending), dry-run the extractor with no writes from the lowest, and take the first with at least five same-package plain-function call sites resolving exact and at least one skipped builtin. Name it by import path and put the ranked table in Results.

What the tracer handles. Identifier calls resolve in this order: local scope, same-package symbol, builtin or conversion, then `no_match`. Local scope is tracked per function (parameters, `:=`, `var`, closure variables), which also decides builtin shadowing. A same-package function is `exact` with kind `func`. A builtin (the fixed list in D-073, unless shadowed) and a conversion are not stored and are counted `skipped_builtin` and `skipped_conversion`; conversions are recognised with an in-memory declared-name index of the package's types, functions, variables and constants, so `type Foo int; Foo(x)` is a conversion. A locally declared identifier call is `deferred_local`. Every call through a selector is `deferred_selector`. An identifier that is none of the above is stored `unresolved` with reason `no_match` (a package-level function variable lands here until ticket 09 reclassifies it). Calls inside function literals belong to the enclosing declared function or method. If locals cannot be told apart from unresolved identifiers in some case, say so in Results.

Storage is one transaction per file: delete that file's tree-sitter edges, insert with conflicts ignored, write the file's content hash and symbol-set fingerprint. The resolver version constant (starting at 1) is introduced here and is part of the fingerprint. A rerun rebuilds the same rows (skipping unchanged files is ticket 03).

`--stats` prints edge counts per resolution and reason (summing to the stored total), per call kind, skip counters and deferred counters, and asserts total call sites = stored + skipped + deferred.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] Migration 00005 (reversible): `call_line`, `call_kind` (func, concrete_method, interface_method, func_value, unknown), `unresolved_reason` (external, no_match, unknown_receiver, func_value, candidates_capped); `resolution` also allows `unresolved`; CHECKs make `unresolved` equivalent to a NULL callee and to a present reason; unique index over source, caller, raw callee, call line and callee-or-zero; callee FK cascades on delete; `graphed_sha` and `graphed_symbols_fp` on files; existing name_only rows with NULL callee are converted to unresolved/no_match first (no-op on an empty table); `make migrate-down migrate-up` leaves `\d edges` unchanged
- [ ] Migration tests: a row breaking each CHECK is rejected; duplicate edge rejected; deleting a callee symbol removes the edge and raises no error
- [ ] Fixture harness: a committed module fixture is walked and parsed into a throwaway database by a shared test helper that later tickets extend
- [ ] Fixture goldens cover: same-package call (exact, func), `len(x)` and `string(b)` and a conversion to a declared non-struct type (skipped, counted), a shadowed builtin (stored), a call in a function literal (attributed to the enclosing function), a locally declared identifier call (`deferred_local`), a selector call (`deferred_selector`), and a call to a missing identifier (`no_match`)
- [ ] `go run ./cmd/indexer graph --package <chosen package> --stats` runs on the real tree: at least five exact edges, at least one skipped builtin, the invariant line passes, and the count of `no_match` is reported (0 is reported as 0)
- [ ] Rerunning the same command gives identical counts; `select count(*) from edges where (resolution='unresolved') <> (callee_id is null)` returns 0
- [ ] Results hold the ranked package table and the chosen package's import path
- [ ] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [ ] One commit `S2-02: ...` that also sets this ticket's Status to done
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Selector calls of any kind, import aliases, external classification, skipping unchanged files and `--status` (ticket 03), the whole repo, function values, any Rule 2 to 5.
