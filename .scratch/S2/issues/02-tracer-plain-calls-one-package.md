# 02: Tracer: plain calls for one package, with migration 00005

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 26-34, 37, 42-45, 61-65, 74-77, 98, 101, 102, 97, 99.

**What to build:** Running `indexer graph --package <import path>` on one small real package produces correct tree-sitter edges for plain function calls and a `--stats` report in which every call site is accounted for. It is the first slice through every layer: the full migration, the fixture harness, extraction, resolution Rule 1, storage and the stats invariant.

The package is chosen by rule: rank in-slice packages by function-plus-method count (ties by import path ascending), dry-run the extractor with no writes from the lowest, and take the first with at least five same-package plain-function call sites resolving exact and at least one skipped builtin. Name it by import path and put the ranked table in Results.

What the tracer handles. Identifier calls resolve in this order: local scope, same-package symbol, builtin or conversion, then `no_match`. Local scope is tracked per function (parameters, `:=`, `var`, closure variables), which also decides builtin shadowing. A same-package function is `exact` with kind `func`. A builtin (the fixed list in D-073, unless shadowed) and a conversion are not stored and are counted `skipped_builtin` and `skipped_conversion`; conversions are recognised with an in-memory declared-name index of the package's types, functions, variables and constants, so `type Foo int; Foo(x)` is a conversion. A locally declared identifier call is `deferred_local`. Every call through a selector is `deferred_selector`. An identifier that is none of the above is stored `unresolved` with reason `no_match` (a package-level function variable lands here until ticket 09 reclassifies it). Calls inside function literals belong to the enclosing declared function or method. If locals cannot be told apart from unresolved identifiers in some case, say so in Results.

Storage is one transaction per file: delete that file's tree-sitter edges, insert with conflicts ignored, write the file's content hash and symbol-set fingerprint. The resolver version constant (starting at 1) is introduced here and is part of the fingerprint. A rerun rebuilds the same rows (skipping unchanged files is ticket 03).

`--stats` prints edge counts per resolution and reason (summing to the stored total), per call kind, skip counters and deferred counters, and asserts total call sites = stored + skipped + deferred.

**Blocked by:** None (can start immediately)

**Status:** done

- [x] Migration 00005 (reversible): `call_line`, `call_kind` (func, concrete_method, interface_method, func_value, unknown), `unresolved_reason` (external, no_match, unknown_receiver, func_value, candidates_capped); `resolution` also allows `unresolved`; CHECKs make `unresolved` equivalent to a NULL callee and to a present reason; unique index over source, caller, raw callee, call line and callee-or-zero; callee FK cascades on delete; `graphed_sha` and `graphed_symbols_fp` on files; existing name_only rows with NULL callee are converted to unresolved/no_match first (no-op on an empty table); `make migrate-down migrate-up` leaves `\d edges` unchanged
- [x] Migration tests: a row breaking each CHECK is rejected; duplicate edge rejected; deleting a callee symbol removes the edge and raises no error
- [x] Fixture harness: a committed module fixture is walked and parsed into a throwaway database by a shared test helper that later tickets extend
- [x] Fixture goldens cover: same-package call (exact, func), `len(x)` and `string(b)` and a conversion to a declared non-struct type (skipped, counted), a shadowed builtin (stored), a call in a function literal (attributed to the enclosing function), a locally declared identifier call (`deferred_local`), a selector call (`deferred_selector`), and a call to a missing identifier (`no_match`)
- [x] `go run ./cmd/indexer graph --package <chosen package> --stats` runs on the real tree: at least five exact edges, at least one skipped builtin, the invariant line passes, and the count of `no_match` is reported (0 is reported as 0)
- [x] Rerunning the same command gives identical counts; `select count(*) from edges where (resolution='unresolved') <> (callee_id is null)` returns 0
- [x] Results hold the ranked package table and the chosen package's import path
- [x] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [x] One commit `S2-02: ...` that also sets this ticket's Status to done
- [x] Report: the commands run (trimmed output) and the commit hash

Out of scope: Selector calls of any kind, import aliases, external classification, skipping unchanged files and `--status` (ticket 03), the whole repo, function values, any Rule 2 to 5.

## Report

Layout:

- `internal/store/migrations/00005_edges_columns.sql` — the reversible migration.
- `internal/graph/` — `version.go` (`ResolverVersion = 1`), `extract.go` (tree-sitter call-site extraction, per-file declared-name index, per-declaration local scope), `resolve.go` (`Index`, Rule 1 `Analyze`, `Fingerprint`), `stats.go` (the reconciliation counters and invariant).
- `cmd/indexer/graph.go` — `indexer graph` with `--package`, `--limit`, `--stats`, `--dry-run`; one transaction per file; symbol index loaded once; SIGINT-safe at file boundaries.
- `cmd/indexer/testdata/graph/{go.mod,plain/plain.go,shadow/shadow.go}` — the committed fixture; `cmd/indexer/graph_test.go` — the shared harness and goldens.
- `internal/store/migrate_integration_test.go` — migration shape/cascade/dedup tests and the down/up descriptor test.
- Docs: `docs/plan/glossary.md` (deferred counters, declared-name index), this ticket, `docs/plan/sprints/S2-call-graph.md` Results.

Migration notes. 00005 adds `files.graphed_sha`/`graphed_symbols_fp`, `edges.call_line` (NOT NULL DEFAULT 0), `call_kind` (NOT NULL DEFAULT 'unknown', five values), `unresolved_reason` (five values), the fourth `resolution` value, the two shape CHECKs, the cascade callee FK and `edges_uniq`. Existing `name_only`/NULL-callee rows are converted first (no-op on the empty table). Down converts `unresolved` back to `name_only` before restoring the three-value CHECK, drops `edges_uniq` and reverses the columns. The down/up cycle is scoped to an empty `edges` table (as the design says): re-adding `call_line` with a default cannot recover the real call lines, so with data present two formerly distinct edges on the same raw/callee could collide on the unique index; the descriptor test asserts the empty-table cycle leaves `\d edges` unchanged.

Package selection. Commands:

```
docker compose exec -T postgres psql -tA -U codesearch -d codesearch -c "select f.package, count(*) from symbols s join files f on f.id=s.file_id where s.kind in ('func','method') and f.in_slice group by 1 order by 2 desc, 1 asc"
go build -o bin/indexer ./cmd/indexer
while ... bin/indexer graph --package <p> --dry-run --stats   # from the lowest count upward
```

Chosen: `k8s.io/kubernetes/pkg/kubelet/envvars` (first of the 294 in-slice packages, from the lowest count upward, with >=5 exact and >=1 skipped builtin; dry run: exact 5, skipped_builtin 12, invariant ok). The ranked table and the real run output are in `docs/plan/sprints/S2-call-graph.md` Results.

Real run and shape (dev DB):

```
$ go run ./cmd/indexer graph --package k8s.io/kubernetes/pkg/kubelet/envvars --stats
2 files to graph
graphed 2 files, stored 5 edges
edges stored: 5
  resolution exact: 5
  resolution name_only: 0
  ...
  reason no_match: 0
...
invariant: call sites 32 = stored 5 + skipped 12 + deferred 15 [ok]
$ psql -c "select count(*) from edges where source='treesitter'"          -> 5
$ psql -c "select count(*) from edges where (resolution='unresolved') <> (callee_id is null)"  -> 0
```

A rerun printed identical counts. `make migrate-down migrate-up` on the dev schema (edges emptied first) left `\d edges` identical.

Tests: `make test` passed with no skips; `make ci-local` passed (vet, tests, golangci-lint v2.14.0 0 issues, shellcheck, actionlint). Ambiguity: a package-level function variable called as a function resolves to `no_match` until function values are handled, as specified; the flat per-declaration local scope can turn an identifier call whose name shadows a nested-literal local into `deferred_local`, never into a wrong stored edge. Commit hash: recorded by the orchestrator's `S2-02: ...` commit (no git command was run by the agent).

### Review follow-up (M1 + cleanups)

The classifier defect M1 is fixed in `internal/graph/extract.go`: every leading identifier name of a `parameter_declaration`/`variadic_parameter_declaration`, `var_spec` and `const_spec` is now collected (the type node is excluded by byte range, because tree-sitter gives each name in `a, b T` its own `name` identifier and a bare type may be an identifier), and grouped declarations are followed through `var_spec_list`/`const_spec_list`. Before the fix a call to a second parameter/var or any grouped name fell through to `no_match` and was stored as a wrong `unresolved` edge; now it is `deferred_local`, so the flat-scope claim above is true.

New fixture cases in `cmd/indexer/testdata/graph/params/params.go` and `cmd/indexer/graph_test.go` (`TestGraphStageLocalDeclarationNames`, expecting 4 `deferred_local`, 0 stored): a second parameter call (`multi(a, b int)` then `b()`), a second name in a plain `var` (`var p, q int` then `q()`), a grouped `var ( x, y = ... )` then `y()`, and a grouped `const ( m, n = ... )` then `n()`.

Cleanups: `Index.Symbols` removed (dead field); `Call.Qualifier` removed (never assigned) and its doc fixed; `--limit` now applies at package boundaries so a package is graphed whole (`TestGraphStageLimitKeepsPackagesWhole`), with `--dry-run` printing `would graph N files, M edges`; migration 00005's Up conversion widened to every non-`unresolved` NULL-callee row; and a migration test now proves a duplicate unresolved row (NULL callee) is rejected by `edges_uniq` via `coalesce(callee_id, 0)`. The real tracer run on `pkg/kubelet/envvars` still reports 5 exact / 12 skipped builtin / 15 deferred selector, invariant ok, exit 0.
