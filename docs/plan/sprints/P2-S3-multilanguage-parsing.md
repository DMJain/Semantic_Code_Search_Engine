# P2-S3 - Multi-language parsing

## Goal
Make any repository indexable: language registry, file filters, generated-file detection, tier-1 symbol extractors and heuristic call graphs (Go, TypeScript/JavaScript, Python, Java), and a fallback chunker for every other text language.

## Entry criteria
P2-S1 done (Store interface, SQLite). The Phase 1 parser, resolver and chunker packages exist.

## Links
[v2-architecture.md#repo-filters](../v2-architecture.md#repo-filters), [#languages](../v2-architecture.md#languages); [architecture.md#stage-parse](../architecture.md#stage-parse), [#stage-graph](../architecture.md#stage-graph), [#stage-chunk](../architecture.md#stage-chunk), [#token-estimate](../architecture.md#token-estimate); decisions D-048, D-049.

## Common contracts
`lang.Extractor` returns `[]Symbol{Kind, Name, QualifiedName, Receiver, Signature, StartLine, EndLine, Doc, RawCalls}` and `[]CallSite{Raw, Qualifier, Name, Line, ReceiverType}` for one file. Qualified names outside Go use `<repo-relative path>#<Container>.<name>`. Resolution values stay `exact | name_only | ambiguous` with one edge row per candidate (D-023).

## Tasks

### P2-S3-T01 - Language registry and detection
- Why: one place that decides how each file is handled.
- Depends on: none
- Touches: `internal/lang/registry.go`, `internal/lang/detect.go`
- Details: `Language{ID, Extensions, Shebangs, Tier 1|0, Grammar func() *sitter.Language (nil if none), Extractor}`; `Detect(path, firstLine)` by extension, then shebang, else `text`. Tier-1 IDs: `go`, `typescript` (`.ts .tsx`), `javascript` (`.js .jsx .mjs .cjs`), `python`, `java`. Registered fallback grammars may be added later without code changes elsewhere.
- Acceptance: table test covering 40 paths including `Dockerfile`, `Makefile`, `.tsx`, a `#!/usr/bin/env python3` file without extension, and an unknown extension (-> `text`); `go test -count=1 ./internal/lang/...` passes.
- Tests: table tests.
- Out of scope: extractors.
- Owner: agent

### P2-S3-T02 - File filters
- Why: "works on any repo" starts with not reading the wrong files.
- Depends on: none
- Touches: `internal/scan/filter.go`, `internal/scan/gitignore.go`
- Details: `scan.Walk(root, cfg) <-chan FileInfo` honoring `.gitignore` (root and nested, negation, directory rules) using a vetted ignore library (record the choice and version in D-059), plus the skips from [v2-architecture.md#repo-filters](../v2-architecture.md#repo-filters): binary (NUL in first 8KB), size over 1MB, lockfile names, minified, default excluded directories (`vendor/`, `node_modules/`, `third_party/`), `.git/`, and symlink loops (never follow directory symlinks). Config globs from `.lodestone/config.toml` apply last. Every skipped file is counted by reason. Each returned `FileInfo` carries `Language` (from T01 `Detect`), `IsTest` (patterns in [v2-architecture.md#repo-filters](../v2-architecture.md#repo-filters)) and `Size`; the indexer stores them in `files.language`, `files.is_test`, `files.size`.
- Acceptance: fixture tree test (`internal/scan/testdata/tree/`) lists exactly the expected files and the expected skip counts per reason, and the `Language`/`IsTest` values for 10 fixture files (including `foo_test.go`, `test_x.py`, `a.spec.ts`, `FooTest.java`); a symlink loop fixture terminates; `go test -count=1 ./internal/scan/...` passes.
- Tests: golden file listing and skip-count JSON.
- Out of scope: generated-file markers (next task).
- Owner: agent

### P2-S3-T03 - Generated-file detection
- Why: generated code pollutes results.
- Depends on: P2-S3-T02
- Touches: `internal/scan/generated.go`
- Details: `IsGenerated(head []byte) (bool, reason string)` over the first 20 lines with the single marker list of [v2-architecture.md#repo-filters](../v2-architecture.md#repo-filters). Wired into `scan.Walk` as a skip reason `generated`.
- Acceptance: table test with 12 positive and 8 negative headers (including a hand-written comment that merely mentions "generated" later in the file, which must not match beyond line 20); Phase 1's Kubernetes `zz_generated` sample is detected.
- Tests: table tests.
- Out of scope: Phase 1's walk (unchanged).
- Owner: agent

### P2-S3-T04 - Extractor interface and Go adapter
- Why: Phase 1 Go support is reused, not rewritten.
- Depends on: P2-S3-T01
- Touches: `internal/lang/extractor.go`, `internal/lang/golang/`
- Details: define `Extractor`; the Go adapter wraps `internal/parse` and the Phase 1 call-site extraction and resolver (`internal/graph`) behind it without changing their behavior. Qualified names keep the Phase 1 Go form. The package import path is the nearest `go.mod` module path plus the relative directory (`vendor/` and `go.work` ignored); Phase 1's Kubernetes mapping (staging paths to `k8s.io/client-go/...`) becomes a configuration of the Kubernetes parity run, not a default.
- Acceptance: a golden test on a fixture with a nested module (`go.mod` in a subdirectory) yields import paths `<module>/<dir>`; golden test: the adapter output for `testdata/fixture-repo` Go files equals the Phase 1 parse output (symbols and call sites) byte for byte; `go test -count=1 ./internal/parse/... ./internal/graph/...` unchanged and passing; `go test -count=1 ./internal/e2e/...` passes with the goldens unchanged.
- Tests: golden comparison.
- Out of scope: other languages.
- Owner: agent

### P2-S3-T05 - TypeScript/JavaScript extractor
- Why: tier-1 language.
- Depends on: P2-S3-T04
- Touches: `internal/lang/typescript/`
- Details: tree-sitter grammars for TypeScript/TSX/JavaScript. Symbols: function declarations, arrow/function expressions assigned to `const`/`let`/`var` or exported, classes, methods (static/private flagged), interfaces, type aliases, enums; doc = preceding JSDoc/`//` comments; qualified name `path#Class.method`. `RawCalls` and `CallSite`s from `call_expression` (member chains kept as written, `new X()` recorded with Qualifier `new`).
- Acceptance: golden files for a fixture with ES modules, CommonJS `require`, classes with inheritance, default exports and TSX components; `go test -count=1 ./internal/lang/typescript/...` passes.
- Tests: golden symbols and call sites.
- Out of scope: type-aware resolution.
- Owner: agent

### P2-S3-T06 - Python extractor
- Why: tier-1 language.
- Depends on: P2-S3-T04
- Touches: `internal/lang/python/`
- Details: symbols: functions, async functions, classes, methods (decorators kept in `Signature`), nested functions flagged; doc = docstring; qualified name `path#Class.method`. Call sites from `call` nodes; `import`/`from ... import ... as` alias map recorded for resolution.
- Acceptance: golden files for a fixture with decorators, nested classes, async defs, `from x import y as z`, and lambda assignments; tests pass.
- Tests: golden.
- Out of scope: type inference.
- Owner: agent

### P2-S3-T07 - Java extractor
- Why: tier-1 language.
- Depends on: P2-S3-T04
- Touches: `internal/lang/java/`
- Details: symbols: classes, interfaces, enums, records, methods, constructors; doc = Javadoc; qualified name `path#Class.method(argTypes)` to separate overloads. Import list recorded; call sites from `method_invocation` and `object_creation_expression`.
- Acceptance: golden files for a fixture with overloads, nested classes, interfaces with default methods, static imports; tests pass.
- Tests: golden.
- Out of scope: classpath-aware resolution.
- Owner: agent

### P2-S3-T08 - Heuristic call resolution for TS/JS and Python
- Why: a usable call graph without a type checker.
- Depends on: P2-S3-T05, P2-S3-T06
- Touches: `internal/lang/resolve_dynamic.go`
- Details: resolution order per file: (1) same-file definitions by name; (2) imported names via the import/alias map to the target file's exported symbol (resolve module paths relative to the importing file; bare package imports stay external); (3) `self.`/`this.` calls to methods of the enclosing class (and its same-file base classes); (4) `obj.method` where `obj`'s class is known from a local `new X()` or `X()` assignment; (5) otherwise all symbols in the repo with that method name: one candidate = `name_only`, several = `ambiguous` rows. Unresolved external calls store a NULL-callee edge with `callee_raw`.
- Acceptance: golden resolution tests per rule for both languages (10 cases each) with the expected resolution values; an ambiguity case produces one row per candidate.
- Tests: golden.
- Out of scope: dynamic dispatch beyond the rules.
- Owner: agent

### P2-S3-T09 - Heuristic call resolution for Java; Go wiring
- Why: complete tier 1.
- Depends on: P2-S3-T07, P2-S3-T04
- Touches: `internal/lang/java/resolve.go`
- Details: Java: same-class methods and fields' declared types (`Foo foo; foo.bar()` resolves to `Foo.bar` when `Foo` is a repo class via imports or same package), `this`/`super`, static calls `Cls.method`, constructors; overloads resolved by argument count (equal count -> `ambiguous` rows). Go uses the Phase 1 resolver unchanged through the adapter.
- Acceptance: golden resolution tests (10 Java cases); the Go adapter's edges for the fixture repo equal Phase 1's.
- Tests: golden.
- Out of scope: generics-aware resolution.
- Owner: agent

### P2-S3-T10 - Fallback chunker and generic chunking for tier 1
- Why: every other language indexes with lexical and semantic search.
- Depends on: P2-S3-T01
- Touches: `internal/chunk/fallback.go`
- Details: if the language has a registered grammar, chunk by top-level named nodes (up to 1500 estimated tokens, same bytes/4 estimate, split at statement boundaries as in Phase 1, using the block node of each language: `statement_block` for TypeScript/JavaScript, `block` for Python and Java); otherwise line windows of 60 lines with 10 overlap, each chunk with the context header (path, language). Symbols are optional here: tier-1 chunks reuse the Phase 1 function-per-chunk logic via the extractor output.
- Acceptance: golden chunking tests for a Rust sample (grammar absent -> line windows), a YAML file, a 5,000-line generated-looking text; no chunk exceeds 1500 estimated tokens except a single oversized line, which is truncated with a marker; `go test -count=1 ./internal/e2e/...` passes with the goldens unchanged.
- Tests: golden files.
- Out of scope: embedding.
- Owner: agent

### P2-S3-T11 - Robustness corpus: any repo indexes without errors
- Why: the product promise.
- Depends on: P2-S3-T02, P2-S3-T03, P2-S3-T10, P2-S3-T08, P2-S3-T09
- Touches: `internal/lang/robustness_test.go`, `internal/lang/testdata/weird/`
- Details: fixtures: empty file, file with only a BOM, CRLF files, invalid UTF-8, a 200KB single line, deeply nested expressions (parser stress), a file that tree-sitter reports ERROR nodes for, mixed-language directory, a file named like a lockfile. A `ParseAll(ctx, files)` driver collects per-file errors in an `IndexReport` and never panics or aborts (panics inside an extractor are recovered and recorded).
- Acceptance: `go test -count=1 ./internal/lang/...` passes the robustness test, which asserts zero aborts, the report lists each failure reason, and every fixture yields at least one chunk or an explicit skip reason.
- Tests: the robustness test.
- Out of scope: the real-repo smoke test (P2-S8).
- Owner: agent

## Measurements to record
- Files per language and skip counts per reason on the fixture and on three small public repos (one per tier-1 language) run through `ParseAll` (Results).
- Parse throughput (files per second) on the Kubernetes slice.

## Exit criteria
All tier-1 extractors and resolvers pass golden tests; the robustness test passes; Phase 1 parse and graph tests unchanged; ROADMAP updated.

## Risks + mitigations
- Grammars need cgo and complicate builds: all CI jobs run on native runners (D-048); build tags are not used.
- Heuristic call graphs are noisy: resolution values and ambiguity are exposed in outputs; no accuracy claim beyond the smoke test.

## Demo check
Run the parse driver over a TS, a Python and a Java checkout and read the index report (symbols, edges, skipped files by reason).

## Results
(empty)
