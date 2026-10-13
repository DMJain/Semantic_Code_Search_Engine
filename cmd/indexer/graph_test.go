package main

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/graph"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
)

// graphFixture walks and parses the committed module fixture into a throwaway
// database. It is the shared harness later S2 tickets extend (spec Seam 1).
func graphFixture(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	pool := testdb.New(t)
	root := filepath.Join("testdata", "graph")
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)
	return pool, root
}

// runGraphStage drives the graph stage and returns its stdout.
func runGraphStage(t *testing.T, pool *pgxpool.Pool, root string, opts graphOptions) string {
	t.Helper()
	var buf bytes.Buffer
	if err := graphStage(context.Background(), pool, root, opts, &buf); err != nil {
		t.Fatalf("graphStage: %v", err)
	}
	return buf.String()
}

// edgeRow is one stored edge joined to its callee's qualified name.
type edgeRow struct {
	resolution string
	reason     string
	kind       string
	raw        string
	line       int
	callee     string
}

// edgesFor returns the tree-sitter edges whose caller has the given qualified
// name, ordered by line then raw text.
func edgesFor(t *testing.T, pool *pgxpool.Pool, callerQN string) []edgeRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT e.resolution, coalesce(e.unresolved_reason, ''), e.call_kind,
		       e.callee_raw, e.call_line, coalesce(c.qualified_name, '')
		FROM edges e
		JOIN symbols caller ON caller.id = e.caller_id
		LEFT JOIN symbols c ON c.id = e.callee_id
		WHERE e.source = 'treesitter' AND caller.qualified_name = $1
		ORDER BY e.call_line, e.callee_raw`, callerQN)
	if err != nil {
		t.Fatalf("query edges: %v", err)
	}
	defer rows.Close()

	var out []edgeRow
	for rows.Next() {
		var e edgeRow
		if err := rows.Scan(&e.resolution, &e.reason, &e.kind, &e.raw, &e.line, &e.callee); err != nil {
			t.Fatalf("scan edge: %v", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("edges rows: %v", err)
	}
	return out
}

// assertEdges compares got with want, reporting the first difference.
func assertEdges(t *testing.T, got, want []edgeRow) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("edge count = %d, want %d\ngot  %+v\nwant %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("edge %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestGraphStagePlainFixtureGoldens covers the plain-call classifications on the
// plain fixture package: a same-package call (exact, func), builtin and
// predeclared-type skips, a conversion to a declared non-struct type, a locally
// declared identifier call, a selector call, a call inside a function literal
// attributed to the enclosing function, and a missing identifier (no_match).
func TestGraphStagePlainFixtureGoldens(t *testing.T) {
	pool, root := graphFixture(t)
	out := runGraphStage(t, pool, root, graphOptions{Package: "k8s.io/kubernetes/plain", Stats: true})

	got := edgesFor(t, pool, "k8s.io/kubernetes/plain.caller")
	want := []edgeRow{
		{"exact", "", "func", "Target", 8, "k8s.io/kubernetes/plain.Target"},
		{"unresolved", "no_match", "unknown", "Missing", 15, ""},
		{"exact", "", "func", "Target", 16, "k8s.io/kubernetes/plain.Target"},
	}
	assertEdges(t, got, want)

	for _, line := range []string{
		"resolution exact: 2",
		"resolution unresolved: 1",
		"reason no_match: 1",
		"reason external: 0",
		"call kind func: 2",
		"call kind unknown: 1",
		"skipped_builtin: 2",
		"skipped_conversion: 1",
		"deferred_local: 1",
		"deferred_selector: 1",
		"deferred_interface: 0",
		"invariant: call sites 8 = stored 3 + skipped 3 + deferred 2 [ok]",
	} {
		if !bytes.Contains([]byte(out), []byte(line)) {
			t.Errorf("stats output missing %q\n%s", line, out)
		}
	}
}

// TestGraphStageLocalDeclarationNames proves every name of a multi-name or
// grouped declaration is a local, so calling it is deferred_local and never a
// stored no_match edge: a second parameter, a second name in a plain var, a
// grouped var and a grouped const.
func TestGraphStageLocalDeclarationNames(t *testing.T) {
	pool, root := graphFixture(t)
	out := runGraphStage(t, pool, root, graphOptions{Package: "k8s.io/kubernetes/params", Stats: true})

	for _, caller := range []string{
		"k8s.io/kubernetes/params.multi",
		"k8s.io/kubernetes/params.localGrouped",
		"k8s.io/kubernetes/params.localMulti",
		"k8s.io/kubernetes/params.localConst",
	} {
		if got := edgesFor(t, pool, caller); len(got) != 0 {
			t.Errorf("edges for %s = %+v, want none (every call is deferred_local)", caller, got)
		}
	}
	for _, line := range []string{
		"deferred_local: 4",
		"invariant: call sites 4 = stored 0 + skipped 0 + deferred 4 [ok]",
	} {
		if !bytes.Contains([]byte(out), []byte(line)) {
			t.Errorf("stats output missing %q\n%s", line, out)
		}
	}
}

// TestGraphStageShadowedBuiltin proves a package function named len shadows the
// builtin, so len(x) is stored exact instead of skipped.
func TestGraphStageShadowedBuiltin(t *testing.T) {
	pool, root := graphFixture(t)
	runGraphStage(t, pool, root, graphOptions{Package: "k8s.io/kubernetes/shadow"})

	got := edgesFor(t, pool, "k8s.io/kubernetes/shadow.use")
	want := []edgeRow{{"exact", "", "func", "len", 6, "k8s.io/kubernetes/shadow.len"}}
	assertEdges(t, got, want)
}

// TestGraphStageAliasGoldens covers Rule 2 and the import alias map on the alias
// fixture package: an aliased import (exact, func) for two packages declaring the
// same name (the collision resolves by alias), a dot import ignored, a blank
// import ignored, a shadowed alias left deferred, an external alias (klog.V),
// a missed symbol in an indexed package (no_match), a conversion to a declared
// non-struct type (skipped_conversion), and a staging-style import path mapped
// to its k8s.io/client-go/... package.
func TestGraphStageAliasGoldens(t *testing.T) {
	pool, root := graphFixture(t)
	out := runGraphStage(t, pool, root, graphOptions{Package: "k8s.io/kubernetes/alias", Stats: true})

	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/alias.callOne"),
		[]edgeRow{{"exact", "", "func", "depone.Name", 15, "k8s.io/kubernetes/depone.Name"}})
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/alias.callTwo"),
		[]edgeRow{{"exact", "", "func", "deptwo.Name", 19, "k8s.io/kubernetes/deptwo.Name"}})

	// Dot import ignored: the dot-imported name is not a same-package function,
	// so it is a stored no_match rather than an exact edge.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/alias.callDot"),
		[]edgeRow{{"unresolved", "no_match", "unknown", "DotOnly", 23, ""}})
	// Blank import ignored: it adds no alias, so the qualifier is unknown and the
	// call stays deferred.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/alias.callBlank"), nil)
	// Conversion to a declared non-struct type is skipped, never stored.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/alias.callConvert"), nil)
	// External: klog is not an indexed package, so the inner klog.V(2) call is
	// stored unresolved external; the outer chained call stays deferred.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/alias.callExternal"),
		[]edgeRow{{"unresolved", "external", "unknown", "klog.V", 35, ""}})
	// A missed symbol in an indexed package is no_match.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/alias.callMissing"),
		[]edgeRow{{"unresolved", "no_match", "unknown", "depone.Missing", 39, ""}})
	// A staging-style import path resolves to its k8s.io/client-go/... package.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/alias.callStaging"),
		[]edgeRow{{"exact", "", "func", "cache.SomeFunc", 43, "k8s.io/client-go/tools/cache.SomeFunc"}})
	// A parameter named like the alias shadows the alias (Rule 2 does not
	// apply), but its own written type *fakeKlog is known, so Rule 3 resolves
	// the inner klog.V to the concrete method; the outer chained call stays
	// deferred.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/alias.callShadowed"),
		[]edgeRow{{"exact", "", "concrete_method", "klog.V", 47, "k8s.io/kubernetes/alias.(fakeKlog).V"}})

	for _, line := range []string{
		"resolution exact: 4",
		"resolution unresolved: 3",
		"reason external: 1",
		"reason no_match: 2",
		"call kind func: 3",
		"call kind concrete_method: 1",
		"call kind unknown: 3",
		"skipped_conversion: 1",
		"deferred_selector: 3",
		"invariant: call sites 11 = stored 7 + skipped 1 + deferred 3 [ok]",
	} {
		if !bytes.Contains([]byte(out), []byte(line)) {
			t.Errorf("stats output missing %q\n%s", line, out)
		}
	}
}

// TestGraphStageMethodGoldens covers Rule 3 (known receivers and parameters) on
// the methods fixture package: a pointer receiver, a value receiver, a
// parameter-typed call, a method name on two types (resolved by receiver type),
// an interface-typed parameter (interface_method, counted deferred_interface, no
// stored row), an embedded struct left unknown, and a parameter shadowed by an
// inner := (unknown, so never a wrong exact).
func TestGraphStageMethodGoldens(t *testing.T) {
	pool, root := graphFixture(t)
	out := runGraphStage(t, pool, root, graphOptions{Package: "k8s.io/kubernetes/methods", Stats: true})

	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/methods.(*Scheduler).Run"),
		[]edgeRow{{"exact", "", "concrete_method", "s.Tick", 6, "k8s.io/kubernetes/methods.(*Scheduler).Tick"}})
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/methods.pointerReceiver"),
		[]edgeRow{{"exact", "", "concrete_method", "s.Run", 36, "k8s.io/kubernetes/methods.(*Scheduler).Run"}})
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/methods.valueReceiver"),
		[]edgeRow{{"exact", "", "concrete_method", "w.Tick", 40, "k8s.io/kubernetes/methods.(Widget).Tick"}})
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/methods.parameterTyped"),
		[]edgeRow{{"exact", "", "concrete_method", "x.M", 44, "k8s.io/kubernetes/methods.(Box).M"}})
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/methods.sameNameOnTwoTypes"),
		[]edgeRow{
			{"exact", "", "concrete_method", "a.M", 48, "k8s.io/kubernetes/methods.(Box).M"},
			{"exact", "", "concrete_method", "c.M", 49, "k8s.io/kubernetes/methods.(Crate).M"},
		})
	// An interface-typed parameter is interface_method and counted
	// deferred_interface; no Rule 4 row is stored yet.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/methods.interfaceParam"), nil)
	// Embedded structs are left unknown: M is promoted from Inner but never seen.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/methods.embeddedUnknown"), nil)
	// A parameter shadowed by an inner := is unknown, so the call is deferred and
	// never a wrong exact.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/methods.shadowedParameter"), nil)
	// A type-switch guard binding a name that shadows a receiver is unknown, so
	// the call inside the case is deferred and never the outer receiver's method.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/methods.typeSwitchShadow"), nil)
	// A receive-statement binding that shadows a receiver is unknown too.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/methods.receiveShadow"), nil)
	// A bare type that is the declaration's own type parameter is never guessed,
	// even when the package declares a type of the same name with that method.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/methods.typeParam"), nil)

	for _, line := range []string{
		"resolution exact: 6",
		"call kind concrete_method: 6",
		"deferred_selector: 5",
		"deferred_interface: 1",
		"invariant: call sites 12 = stored 6 + skipped 0 + deferred 6 [ok]",
	} {
		if !bytes.Contains([]byte(out), []byte(line)) {
			t.Errorf("stats output missing %q\n%s", line, out)
		}
	}
}

// TestGraphResolverVersionBumpStalesFiles proves the resolver version is part of
// the freshness fingerprint (D-076): bumping it changes the fingerprint, a file
// graphed at the previous version's fingerprint is stale (symbols changed), and
// a fresh graph stores the current version's fingerprint.
func TestGraphResolverVersionBumpStalesFiles(t *testing.T) {
	pool, root := graphFixture(t)
	runGraphStage(t, pool, root, graphOptions{})

	symbols, err := loadSymbols(context.Background(), pool)
	if err != nil {
		t.Fatalf("loadSymbols: %v", err)
	}
	names, err := buildNameIndex(context.Background(), pool, root)
	if err != nil {
		t.Fatalf("buildNameIndex: %v", err)
	}
	oldFP := graph.Fingerprint(symbols, names, graph.ResolverVersion-1)
	newFP := graph.Fingerprint(symbols, names, graph.ResolverVersion)
	if oldFP == newFP {
		t.Fatal("bumping the resolver version did not change the fingerprint")
	}

	var storedFP string
	if err := pool.QueryRow(context.Background(),
		`SELECT graphed_symbols_fp FROM files WHERE path = 'plain/plain.go'`).Scan(&storedFP); err != nil {
		t.Fatalf("read stored fingerprint: %v", err)
	}
	if storedFP != newFP {
		t.Errorf("stored fingerprint = %q, want the current version's %q", storedFP, newFP)
	}

	sha := "same-content"
	f := graphFile{id: 1, path: "plain/plain.go", pkg: "k8s.io/kubernetes/plain",
		sha: sha, graphedSHA: &sha, graphedFP: &oldFP}
	if got := staleReasons(f, newFP); len(got) != 1 || got[0] != reasonSymbolsChange {
		t.Errorf("staleReasons with the previous version's fingerprint = %v, want [%s]", got, reasonSymbolsChange)
	}
}

// TestGraphStalenessDeclaredNameChange proves the fingerprint covers the
// declared-name index (D-076 amended to include declared names): adding a
// non-struct type (which is not stored as a symbol) to the aliased package
// changes the fingerprint, so a file that stored a no_match for `dep.Thing(x)`
// is reported stale (symbols changed), and after re-graphing the call is a
// skipped conversion with no stored edge.
func TestGraphStalenessDeclaredNameChange(t *testing.T) {
	root := t.TempDir()
	writeSrc(t, root, "p/dep/dep.go", "package dep\n")
	writeSrc(t, root, "p/use/use.go",
		"package use\n\nimport dep \"k8s.io/kubernetes/p/dep\"\n\nfunc F() {\n\tdep.Thing(1)\n}\n")
	pool := testdb.New(t)
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)
	runGraphStage(t, pool, root, graphOptions{})

	got := edgesFor(t, pool, "k8s.io/kubernetes/p/use.F")
	want := []edgeRow{{"unresolved", "no_match", "unknown", "dep.Thing", 6, ""}}
	assertEdges(t, got, want)

	rewriteAndReparse(t, pool, root, "p/dep/dep.go", "package dep\n\ntype Thing int\n")

	status := runGraphStage(t, pool, root, graphOptions{Status: true})
	for _, line := range []string{
		"2 files stale",
		"content changed: 1",
		"symbols changed: 2",
		"use.go: symbols changed",
		"dep.go: content changed, symbols changed",
	} {
		if !strings.Contains(status, line) {
			t.Errorf("status missing %q after a declared-name-only change:\n%s", line, status)
		}
	}

	runGraphStage(t, pool, root, graphOptions{})
	if got := edgesFor(t, pool, "k8s.io/kubernetes/p/use.F"); len(got) != 0 {
		t.Errorf("after the conversion is declared, edges for use.F = %+v, want none", got)
	}
	last := runGraphStage(t, pool, root, graphOptions{Status: true})
	if !strings.Contains(last, "0 files stale") {
		t.Errorf("a settled tree should report 0 files stale:\n%s", last)
	}
}

// TestGraphStageRerunIdempotent proves a rerun of a fully graphed tree is a
// no-op: the second run prints 0 files to graph, rebuilds nothing, leaves the
// rows unchanged, and the shape rule holds in the database.
func TestGraphStageRerunIdempotent(t *testing.T) {
	pool, root := graphFixture(t)
	opts := graphOptions{Stats: true} // every file, all fixture packages

	first := runGraphStage(t, pool, root, opts)
	before := edgesFor(t, pool, "k8s.io/kubernetes/plain.caller")
	second := runGraphStage(t, pool, root, opts)
	after := edgesFor(t, pool, "k8s.io/kubernetes/plain.caller")

	filesN := scalarInt(t, pool, `SELECT count(*) FROM files`)
	if want := fmt.Sprintf("%d files to graph", filesN); !strings.Contains(first, want) {
		t.Errorf("first run should report %q\n%s", want, first)
	}
	if !strings.Contains(second, "0 files to graph") {
		t.Errorf("second run should report 0 files to graph\n%s", second)
	}
	if !strings.Contains(second, "graphed 0 files, stored 0 edges") {
		t.Errorf("second run should graph nothing\n%s", second)
	}
	assertEdges(t, after, before)

	if n := scalarInt(t, pool,
		`SELECT count(*) FROM edges WHERE (resolution = 'unresolved') <> (callee_id IS NULL)`); n != 0 {
		t.Errorf("rows breaking the unresolved shape = %d, want 0", n)
	}
	if n := scalarInt(t, pool, `SELECT count(*) FROM edges WHERE source = 'treesitter'`); n != 17 {
		t.Errorf("tree-sitter edge count = %d, want 17", n)
	}
}

// TestGraphStageInterruptedThenRerunSameCount proves an interrupted run (here
// simulated with --limit, as the parseCorpus tests do) followed by a full run
// ends with the same tree-sitter edge count as an uninterrupted full run: the
// per-file transaction leaves no partial file and everything else stays stale.
func TestGraphStageInterruptedThenRerunSameCount(t *testing.T) {
	unbroken, root := graphFixture(t)
	runGraphStage(t, unbroken, root, graphOptions{})
	want := scalarInt(t, unbroken, `SELECT count(*) FROM edges WHERE source = 'treesitter'`)

	interrupted := testdb.New(t)
	root2 := filepath.Join("testdata", "graph")
	seedFiles(t, interrupted, root2)
	runParseCorpus(t, interrupted, root2, 0)
	runGraphStage(t, interrupted, root2, graphOptions{Limit: 1})
	runGraphStage(t, interrupted, root2, graphOptions{})
	got := scalarInt(t, interrupted, `SELECT count(*) FROM edges WHERE source = 'treesitter'`)

	if got != want {
		t.Errorf("edge count after interrupt-and-rerun = %d, want %d", got, want)
	}
	last := runGraphStage(t, interrupted, root2, graphOptions{})
	if !strings.Contains(last, "0 files to graph") {
		t.Errorf("a settled rerun should print 0 files to graph\n%s", last)
	}
}

// TestGraphStageLimitStaleFilesUseWholePackageIndex proves --limit counts stale
// files written, so a package can be stopped mid-way, but its declared-name
// index still covers all its files: here the type Foo declared in a second file
// of the same package turns Foo(x) into a conversion even though only the first
// file is graphed.
func TestGraphStageLimitStaleFilesUseWholePackageIndex(t *testing.T) {
	root := t.TempDir()
	writeSrc(t, root, "p/a/a.go", "package a\n\nfunc use(x Foo) {\n\tFoo(x)\n}\n")
	writeSrc(t, root, "p/a/b.go", "package a\n\ntype Foo int\n")
	writeSrc(t, root, "p/b/b.go", "package b\n\nfunc B() {}\n")

	limited := testdb.New(t)
	seedFiles(t, limited, root)
	runParseCorpus(t, limited, root, 0)
	out := runGraphStage(t, limited, root, graphOptions{Limit: 1, Stats: true})

	if got := edgesFor(t, limited, "k8s.io/kubernetes/p/a.use"); len(got) != 0 {
		t.Errorf("edges for a.use under --limit 1 = %+v, want none (Foo is a conversion)", got)
	}
	if !bytes.Contains([]byte(out), []byte("skipped_conversion: 1")) {
		t.Errorf("--limit 1 output missing skipped_conversion: 1\n%s", out)
	}
	if n := scalarInt(t, limited,
		`SELECT count(*) FROM files WHERE package = 'k8s.io/kubernetes/p/a' AND graphed_sha IS NOT NULL`); n != 1 {
		t.Errorf("package a files graphed under --limit 1 = %d, want 1", n)
	}
	if n := scalarInt(t, limited,
		`SELECT count(*) FROM files WHERE package = 'k8s.io/kubernetes/p/b' AND graphed_sha IS NOT NULL`); n != 0 {
		t.Errorf("package b graphed under --limit 1 = %d files, want 0", n)
	}

	// After the limit stops, the rest is still stale and a full run finishes it.
	if n := scalarInt(t, limited, `SELECT count(*) FROM files WHERE graphed_sha IS NULL`); n != 2 {
		t.Errorf("files left stale under --limit 1 = %d, want 2", n)
	}

	// Without the limit the same package's rows are produced.
	full := testdb.New(t)
	seedFiles(t, full, root)
	runParseCorpus(t, full, root, 0)
	runGraphStage(t, full, root, graphOptions{Stats: true})
	assertEdges(t, edgesFor(t, full, "k8s.io/kubernetes/p/a.use"), edgesFor(t, limited, "k8s.io/kubernetes/p/a.use"))
}

// rewriteAndReparse rewrites one source file, re-walks (so files.sha tracks the
// edit) and re-parses the corpus, exactly as the pipeline would after an edit.
func rewriteAndReparse(t *testing.T, pool *pgxpool.Pool, root, rel, content string) {
	t.Helper()
	writeSrc(t, root, rel, content)
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)
}

// TestGraphStalenessCalleeRemoved covers case (i): after a callee is removed and
// re-parsed, the cascade deletes its edge with no CHECK violation, and --status
// reports the caller's content-unchanged file stale with reason symbols changed.
func TestGraphStalenessCalleeRemoved(t *testing.T) {
	root := t.TempDir()
	writeSrc(t, root, "p/a/caller.go", "package a\n\nfunc Caller() {\n\tCallee()\n}\n")
	writeSrc(t, root, "p/a/callee.go", "package a\n\nfunc Callee() {}\n")
	pool := testdb.New(t)
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)
	runGraphStage(t, pool, root, graphOptions{Stats: true})

	if got := edgesFor(t, pool, "k8s.io/kubernetes/p/a.Caller"); len(got) != 1 {
		t.Fatalf("edges before removal = %+v, want 1", got)
	}

	rewriteAndReparse(t, pool, root, "p/a/callee.go", "package a\n")

	if n := scalarInt(t, pool,
		`SELECT count(*) FROM edges WHERE (resolution = 'unresolved') <> (callee_id IS NULL)`); n != 0 {
		t.Errorf("rows breaking the unresolved shape after cascade = %d, want 0", n)
	}
	if got := edgesFor(t, pool, "k8s.io/kubernetes/p/a.Caller"); len(got) != 0 {
		t.Errorf("edges after callee removal = %+v, want none", got)
	}
	status := runGraphStage(t, pool, root, graphOptions{Status: true})
	if !strings.Contains(status, "caller.go: symbols changed") {
		t.Errorf("status should report caller.go stale (symbols changed):\n%s", status)
	}
	// callee.go is both content- and symbols-changed, so the independent
	// per-reason counts sum above the file total; the flags list both.
	for _, want := range []string{
		"2 files stale",
		"content changed: 1",
		"symbols changed: 2",
		"callee.go: content changed, symbols changed",
	} {
		if !strings.Contains(status, want) {
			t.Errorf("status missing %q:\n%s", want, status)
		}
	}
}

// TestGraphStalenessSymbolAdded covers case (ii): adding a symbol that makes a
// previously no_match identifier resolvable changes the fingerprint, so a file
// whose content did not change is still reported stale (symbols changed).
func TestGraphStalenessSymbolAdded(t *testing.T) {
	root := t.TempDir()
	writeSrc(t, root, "p/a/a.go", "package a\n\nfunc caller() {\n\tMissing()\n}\n")
	writeSrc(t, root, "p/a/z.go", "package a\n\nfunc Other() {}\n")
	pool := testdb.New(t)
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)
	runGraphStage(t, pool, root, graphOptions{})

	if got := edgesFor(t, pool, "k8s.io/kubernetes/p/a.caller"); len(got) != 1 || got[0].reason != "no_match" {
		t.Fatalf("edges before = %+v, want one no_match", got)
	}

	rewriteAndReparse(t, pool, root, "p/a/a.go",
		"package a\n\nfunc caller() {\n\tMissing()\n}\n\nfunc Missing() {}\n")

	status := runGraphStage(t, pool, root, graphOptions{Status: true})
	if !strings.Contains(status, "2 files stale") {
		t.Errorf("status should report all files stale:\n%s", status)
	}
	if !strings.Contains(status, "z.go: symbols changed") {
		t.Errorf("a content-unchanged file should be stale (symbols changed):\n%s", status)
	}
	// a.go is both content- and symbols-changed; the counts are independent.
	for _, want := range []string{
		"content changed: 1",
		"symbols changed: 2",
		"a.go: content changed, symbols changed",
	} {
		if !strings.Contains(status, want) {
			t.Errorf("status missing %q:\n%s", want, status)
		}
	}

	runGraphStage(t, pool, root, graphOptions{})
	got := edgesFor(t, pool, "k8s.io/kubernetes/p/a.caller")
	want := []edgeRow{{"exact", "", "func", "Missing", 4, "k8s.io/kubernetes/p/a.Missing"}}
	assertEdges(t, got, want)
}

// TestGraphStalenessBodyOnlyEdit covers case (iii): a body-only edit makes only
// the edited file stale (content changed); the symbol set is unchanged, so no
// other file goes stale.
func TestGraphStalenessBodyOnlyEdit(t *testing.T) {
	root := t.TempDir()
	writeSrc(t, root, "p/a/x.go", "package a\n\nfunc F() {\n\tG()\n}\n\nfunc G() {}\n")
	writeSrc(t, root, "p/a/y.go", "package a\n\nfunc Y() {}\n")
	pool := testdb.New(t)
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)
	runGraphStage(t, pool, root, graphOptions{})

	rewriteAndReparse(t, pool, root, "p/a/x.go", "package a\n\nfunc F() {\n\tG()\n\t_ = 1\n}\n\nfunc G() {}\n")

	status := runGraphStage(t, pool, root, graphOptions{Status: true})
	for _, want := range []string{
		"1 files stale",
		"content changed: 1",
		"symbols changed: 0",
		"x.go: content changed",
	} {
		if !strings.Contains(status, want) {
			t.Errorf("status missing %q:\n%s", want, status)
		}
	}
	if strings.Contains(status, "y.go") {
		t.Errorf("an unchanged file should not be listed stale:\n%s", status)
	}
}

// TestGraphStatusNeverGraphed proves --status names never-graphed files and
// prints 0 files stale once the tree is graphed.
func TestGraphStatusNeverGraphed(t *testing.T) {
	pool, root := graphFixture(t)

	before := runGraphStage(t, pool, root, graphOptions{Status: true})
	if !strings.Contains(before, "11 files stale") || !strings.Contains(before, "never graphed: 11") {
		t.Errorf("status before graphing should list 11 never-graphed files:\n%s", before)
	}
	if !strings.Contains(before, "plain.go: never graphed") {
		t.Errorf("status should name the never-graphed file:\n%s", before)
	}

	runGraphStage(t, pool, root, graphOptions{})
	after := runGraphStage(t, pool, root, graphOptions{Status: true})
	if !strings.Contains(after, "0 files stale") {
		t.Errorf("status after graphing should report 0 files stale:\n%s", after)
	}
}

// TestGraphParseLeavesGraphState proves `parse` never writes graph state: a real
// re-parse after a body edit leaves the stored edges and both freshness markers
// as they were. (The cascade FK of D-076/story 31 removes a vanished callee's
// edge, which is exercised by TestGraphStalenessCalleeRemoved, not here.)
func TestGraphParseLeavesGraphState(t *testing.T) {
	root := t.TempDir()
	writeSrc(t, root, "p/a/x.go", "package a\n\nfunc F() {\n\tG()\n}\n\nfunc G() {}\n")
	pool := testdb.New(t)
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)
	runGraphStage(t, pool, root, graphOptions{})

	edgesBefore := scalarInt(t, pool, `SELECT count(*) FROM edges WHERE source = 'treesitter'`)
	if edgesBefore == 0 {
		t.Fatal("graph stored no edges; the test would pass vacuously")
	}
	var shaBefore, fpBefore string
	if err := pool.QueryRow(context.Background(),
		`SELECT coalesce(graphed_sha, ''), coalesce(graphed_symbols_fp, '') FROM files WHERE path = 'p/a/x.go'`).
		Scan(&shaBefore, &fpBefore); err != nil {
		t.Fatalf("read graph markers: %v", err)
	}
	if shaBefore == "" || fpBefore == "" {
		t.Fatal("file was not marked graphed")
	}

	rewriteAndReparse(t, pool, root, "p/a/x.go", "package a\n\nfunc F() {\n\tG()\n\t_ = 1\n}\n\nfunc G() {}\n")

	if got := scalarInt(t, pool, `SELECT count(*) FROM edges WHERE source = 'treesitter'`); got != edgesBefore {
		t.Errorf("edges after re-parse = %d, want %d", got, edgesBefore)
	}
	var shaAfter, fpAfter string
	if err := pool.QueryRow(context.Background(),
		`SELECT coalesce(graphed_sha, ''), coalesce(graphed_symbols_fp, '') FROM files WHERE path = 'p/a/x.go'`).
		Scan(&shaAfter, &fpAfter); err != nil {
		t.Fatalf("read graph markers after re-parse: %v", err)
	}
	if shaAfter != shaBefore || fpAfter != fpBefore {
		t.Errorf("parse changed graph markers: sha %q->%q, fp %q->%q",
			shaBefore, shaAfter, fpBefore, fpAfter)
	}
}
