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
		ORDER BY e.call_line, e.callee_raw, coalesce(c.qualified_name, '')`, callerQN)
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
// declared identifier call (a function value: unresolved func_value), a selector
// call, a call inside a function literal attributed to the enclosing function,
// and a missing identifier (no_match).
func TestGraphStagePlainFixtureGoldens(t *testing.T) {
	pool, root := graphFixture(t)
	out := runGraphStage(t, pool, root, graphOptions{Package: "k8s.io/kubernetes/plain", Stats: true})

	got := edgesFor(t, pool, "k8s.io/kubernetes/plain.caller")
	want := []edgeRow{
		{"exact", "", "func", "Target", 8, "k8s.io/kubernetes/plain.Target"},
		{"unresolved", "func_value", "func_value", "x", 13, ""},
		{"unresolved", "unknown_receiver", "unknown", "a.Method", 14, ""},
		{"unresolved", "no_match", "unknown", "Missing", 15, ""},
		{"exact", "", "func", "Target", 16, "k8s.io/kubernetes/plain.Target"},
	}
	assertEdges(t, got, want)

	for _, line := range []string{
		"resolution exact: 2",
		"resolution unresolved: 3",
		"reason no_match: 1",
		"reason unknown_receiver: 1",
		"reason func_value: 1",
		"reason external: 0",
		"call kind func: 2",
		"call kind func_value: 1",
		"call kind unknown: 2",
		"skipped_builtin: 2",
		"skipped_conversion: 1",
		"invariant: call sites 8 = stored 5 + skipped 3 [ok]",
	} {
		if !bytes.Contains([]byte(out), []byte(line)) {
			t.Errorf("stats output missing %q\n%s", line, out)
		}
	}
}

// TestGraphStageLocalDeclarationNames proves every name of a multi-name or
// grouped declaration is a local, so calling it goes through a function value
// (unresolved func_value) and is never a stored no_match edge: a second
// parameter, a second name in a plain var, a grouped var and a grouped const.
func TestGraphStageLocalDeclarationNames(t *testing.T) {
	pool, root := graphFixture(t)
	out := runGraphStage(t, pool, root, graphOptions{Package: "k8s.io/kubernetes/params", Stats: true})

	want := map[string]edgeRow{
		"k8s.io/kubernetes/params.multi":        {"unresolved", "func_value", "func_value", "b", 4, ""},
		"k8s.io/kubernetes/params.localGrouped": {"unresolved", "func_value", "func_value", "y", 11, ""},
		"k8s.io/kubernetes/params.localMulti":   {"unresolved", "func_value", "func_value", "q", 17, ""},
		"k8s.io/kubernetes/params.localConst":   {"unresolved", "func_value", "func_value", "n", 25, ""},
	}
	for caller, row := range want {
		assertEdges(t, edgesFor(t, pool, caller), []edgeRow{row})
	}
	for _, line := range []string{
		"reason func_value: 4",
		"call kind func_value: 4",
		"invariant: call sites 4 = stored 4 + skipped 0 [ok]",
	} {
		if !bytes.Contains([]byte(out), []byte(line)) {
			t.Errorf("stats output missing %q\n%s", line, out)
		}
	}
}

// TestGraphStageFuncValues covers the func_value classification over the
// funcvalues fixture package (spec story 59): a func-typed local (`var f
// func()`), a func-typed parameter, a closure variable, a package-level function
// variable with a written function type, one inferred from a function literal,
// one whose type names a package-level function type, and a cross-package
// function variable called through an import alias are all stored unresolved
// with reason func_value, kind func_value and no callee; a package-level
// declared function stays Rule 1 exact (the guard against over-reclassification).
func TestGraphStageFuncValues(t *testing.T) {
	pool, root := graphFixture(t)
	out := runGraphStage(t, pool, root, graphOptions{Package: "k8s.io/kubernetes/funcvalues", Stats: true})

	for _, tc := range []struct {
		caller string
		raw    string
		line   int
	}{
		{"k8s.io/kubernetes/funcvalues.localFuncVar", "f", 17},
		{"k8s.io/kubernetes/funcvalues.paramFunc", "f", 21},
		{"k8s.io/kubernetes/funcvalues.closureVar", "f", 26},
		{"k8s.io/kubernetes/funcvalues.packageVar", "PackageVarFunc", 30},
		{"k8s.io/kubernetes/funcvalues.inferredVar", "InferredFunc", 34},
		{"k8s.io/kubernetes/funcvalues.namedVar", "NamedFunc", 38},
		{"k8s.io/kubernetes/funcvalues.aliasVar", "depone.Hook", 42},
	} {
		assertEdges(t, edgesFor(t, pool, tc.caller),
			[]edgeRow{{"unresolved", "func_value", "func_value", tc.raw, tc.line, ""}})
	}
	// A package-level FUNCTION remains Rule 1 exact, never func_value.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/funcvalues.packageFunc"),
		[]edgeRow{{"exact", "", "func", "PackageFunc", 46, "k8s.io/kubernetes/funcvalues.PackageFunc"}})

	for _, line := range []string{
		"reason func_value: 7",
		"call kind func_value: 7",
		"invariant: call sites 8 = stored 8 + skipped 0 [ok]",
	} {
		if !bytes.Contains([]byte(out), []byte(line)) {
			t.Errorf("stats output missing %q\n%s", line, out)
		}
	}
}

// TestGraphStageAllUnresolvedReasonsOccur proves every value of the
// unresolved_reason vocabulary occurs at least once in a whole-fixture run
// (spec story 98): external, no_match, unknown_receiver, func_value and
// candidates_capped. The small Rule 4 cap (Rule4N 3) is needed to produce a
// candidates_capped row from the four-implementer Cap4 fixture.
func TestGraphStageAllUnresolvedReasonsOccur(t *testing.T) {
	pool, root := graphFixture(t)
	out := runGraphStage(t, pool, root, graphOptions{Rule4N: 3, Stats: true})

	rows, err := pool.Query(context.Background(),
		`SELECT DISTINCT unresolved_reason FROM edges
		 WHERE source = 'treesitter' AND resolution = 'unresolved'`)
	if err != nil {
		t.Fatalf("query reasons: %v", err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var reason string
		if err := rows.Scan(&reason); err != nil {
			t.Fatalf("scan reason: %v", err)
		}
		got[reason] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reasons rows: %v", err)
	}
	want := []string{
		graph.ReasonExternal, graph.ReasonNoMatch, graph.ReasonUnknownReceiver,
		graph.ReasonFuncValue, graph.ReasonCandidatesCap,
	}
	for _, reason := range want {
		if !got[reason] {
			t.Errorf("unresolved reason %q never occurred in the fixture run\n%s", reason, out)
		}
	}
	if len(got) != len(want) {
		t.Errorf("distinct unresolved reasons = %v, want exactly %v", got, want)
	}
}

// btreeIndexTupleLimit is Postgres' maximum btree index entry size for the
// default 8 KB page (~1/3 page); edges_uniq holds callee_raw text, so a longer
// value fails the insert.
const btreeIndexTupleLimit = 2704

// TestGraphStageLongCallRawFitsIndex proves a call whose raw text is huge (an
// immediately-invoked function literal) is still graphed: the stored callee_raw
// is capped so it fits the edges_uniq btree index, rather than failing the whole
// run on a long expression.
func TestGraphStageLongCallRawFitsIndex(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	b.WriteString("package a\n\nfunc F() {\n\tfunc() {\n")
	for i := 0; i < 500; i++ {
		b.WriteString("\t\t_ = 1\n")
	}
	b.WriteString("\t}()\n}\n")
	writeSrc(t, root, "p/a/a.go", b.String())

	pool := testdb.New(t)
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)
	out := runGraphStage(t, pool, root, graphOptions{Stats: true})

	if !strings.Contains(out, "graphed 1 files, stored 1 edges") {
		t.Errorf("expected the long call to be stored once:\n%s", out)
	}
	if n := scalarInt(t, pool,
		`SELECT count(*) FROM edges WHERE source = 'treesitter'`); n != 1 {
		t.Errorf("tree-sitter edge count = %d, want 1", n)
	}
	if n := scalarInt(t, pool,
		`SELECT max(length(callee_raw)) FROM edges WHERE source = 'treesitter'`); n <= 0 || n >= btreeIndexTupleLimit {
		t.Errorf("stored callee_raw length = %d, want a positive value under the btree limit of %d",
			n, btreeIndexTupleLimit)
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
// import ignored, a shadowed alias resolved by its receiver type, an external alias (klog.V),
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
	// Blank import ignored: it adds no alias, so the qualifier is unknown and,
	// under Rule 5, the method name is looked up in the caller's package; there
	// is no BlankOnly method, so the call is unresolved unknown_receiver.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/alias.callBlank"),
		[]edgeRow{{"unresolved", "unknown_receiver", "unknown", "blankpkg.BlankOnly", 27, ""}})
	// Conversion to a declared non-struct type is skipped, never stored.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/alias.callConvert"), nil)
	// External: klog is not an indexed package, so the inner klog.V(2) call is
	// stored unresolved external; the outer chained call has an unknown receiver
	// and Rule 5 finds exactly one same-package method named InfoS, so it is
	// stored name_only.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/alias.callExternal"),
		[]edgeRow{
			{"unresolved", "external", "unknown", "klog.V", 35, ""},
			{"name_only", "", "unknown", "klog.V(2).InfoS", 35, "k8s.io/kubernetes/alias.(fakeKlog).InfoS"},
		})
	// A missed symbol in an indexed package is no_match.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/alias.callMissing"),
		[]edgeRow{{"unresolved", "no_match", "unknown", "depone.Missing", 39, ""}})
	// A staging-style import path resolves to its k8s.io/client-go/... package.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/alias.callStaging"),
		[]edgeRow{{"exact", "", "func", "cache.SomeFunc", 43, "k8s.io/client-go/tools/cache.SomeFunc"}})
	// A parameter named like the alias shadows the alias (Rule 2 does not
	// apply), but its own written type *fakeKlog is known, so Rule 3 resolves
	// the inner klog.V to the concrete method; the outer chained call has an
	// unknown receiver, and Rule 5 finds the one same-package InfoS method.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/alias.callShadowed"),
		[]edgeRow{
			{"exact", "", "concrete_method", "klog.V", 47, "k8s.io/kubernetes/alias.(fakeKlog).V"},
			{"name_only", "", "unknown", "klog.V(2).InfoS", 47, "k8s.io/kubernetes/alias.(fakeKlog).InfoS"},
		})

	for _, line := range []string{
		"resolution exact: 4",
		"resolution name_only: 2",
		"resolution unresolved: 4",
		"reason external: 1",
		"reason no_match: 2",
		"reason unknown_receiver: 1",
		"call kind func: 3",
		"call kind concrete_method: 1",
		"call kind unknown: 6",
		"skipped_conversion: 1",
		"invariant: call sites 11 = stored 10 + skipped 1 [ok]",
	} {
		if !bytes.Contains([]byte(out), []byte(line)) {
			t.Errorf("stats output missing %q\n%s", line, out)
		}
	}
}

// TestGraphStageMethodGoldens covers Rule 3 (known receivers and parameters) on
// the methods fixture package: a pointer receiver, a value receiver, a
// parameter-typed call, a method name on two types (resolved by receiver type),
// an interface-typed parameter with no indexed implementer (interface_method,
// unresolved no_match), an embedded struct left unknown, and a parameter
// shadowed by an inner := (unknown, so never a wrong exact).
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
	// An interface-typed parameter with a known method but no indexed
	// implementer is stored once as interface_method, unresolved no_match.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/methods.interfaceParam"),
		[]edgeRow{{"unresolved", "no_match", "interface_method", "i.IMethod", 53, ""}})
	// Embedded structs are left unknown: M is promoted from Inner but never
	// seen, so the receiver is unknown and Rule 5 stores one ambiguous row per
	// same-package method named M.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/methods.embeddedUnknown"),
		[]edgeRow{
			{"ambiguous", "", "unknown", "o.M", 57, "k8s.io/kubernetes/methods.(Box).M"},
			{"ambiguous", "", "unknown", "o.M", 57, "k8s.io/kubernetes/methods.(Crate).M"},
			{"ambiguous", "", "unknown", "o.M", 57, "k8s.io/kubernetes/methods.(Inner).M"},
			{"ambiguous", "", "unknown", "o.M", 57, "k8s.io/kubernetes/methods.(T).M"},
		})
	// A parameter shadowed by an inner := is unknown: Rule 5 again, never a
	// wrong exact.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/methods.shadowedParameter"),
		[]edgeRow{
			{"ambiguous", "", "unknown", "x.M", 62, "k8s.io/kubernetes/methods.(Box).M"},
			{"ambiguous", "", "unknown", "x.M", 62, "k8s.io/kubernetes/methods.(Crate).M"},
			{"ambiguous", "", "unknown", "x.M", 62, "k8s.io/kubernetes/methods.(Inner).M"},
			{"ambiguous", "", "unknown", "x.M", 62, "k8s.io/kubernetes/methods.(T).M"},
		})
	// A type-switch guard binding a name that shadows a receiver is unknown:
	// Rule 5 finds both Tick methods.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/methods.typeSwitchShadow"),
		[]edgeRow{
			{"ambiguous", "", "unknown", "s.Tick", 68, "k8s.io/kubernetes/methods.(*Scheduler).Tick"},
			{"ambiguous", "", "unknown", "s.Tick", 68, "k8s.io/kubernetes/methods.(Widget).Tick"},
		})
	// A receive-statement binding that shadows a receiver is unknown too; Rule 5
	// finds the one Run method, so it is a name_only guess with a callee.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/methods.receiveShadow"),
		[]edgeRow{{"name_only", "", "unknown", "s.Run", 75, "k8s.io/kubernetes/methods.(*Scheduler).Run"}})
	// A bare type that is the declaration's own type parameter is never guessed
	// as a concrete type, so Rule 5 stores one ambiguous row per same-name
	// method.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/methods.typeParam"),
		[]edgeRow{
			{"ambiguous", "", "unknown", "x.M", 84, "k8s.io/kubernetes/methods.(Box).M"},
			{"ambiguous", "", "unknown", "x.M", 84, "k8s.io/kubernetes/methods.(Crate).M"},
			{"ambiguous", "", "unknown", "x.M", 84, "k8s.io/kubernetes/methods.(Inner).M"},
			{"ambiguous", "", "unknown", "x.M", 84, "k8s.io/kubernetes/methods.(T).M"},
		})

	for _, line := range []string{
		"resolution exact: 6",
		"resolution name_only: 1",
		"resolution ambiguous: 14",
		"resolution unresolved: 1",
		"call kind concrete_method: 6",
		"call kind interface_method: 1",
		"call kind unknown: 15",
		"invariant: call sites 22 = stored 22 + skipped 0 [ok]",
	} {
		if !bytes.Contains([]byte(out), []byte(line)) {
			t.Errorf("stats output missing %q\n%s", line, out)
		}
	}
}

// TestGraphStageLocalsGoldens covers the inferred local type environment and
// same-package facts (spec story 39): `var x T`, `var x = T{}`, `x := T{}`,
// `x := &T{}` and a single-result constructor all resolve via Rule 3; a
// two-result constructor stays unknown (Rule 5); a struct field chain
// `s.cache.Assume` resolves through the field type; an embedded struct's
// promoted method is unseen (Rule 5); a shadowed local is unknown (Rule 5); and
// a method chain `a.B().C()` has an unknown receiver for the outer call (Rule
// 5) while the inner call is Rule 3.
func TestGraphStageLocalsGoldens(t *testing.T) {
	pool, root := graphFixture(t)
	out := runGraphStage(t, pool, root, graphOptions{Package: "k8s.io/kubernetes/locals", Stats: true})

	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/locals.varDecl"),
		[]edgeRow{{"exact", "", "concrete_method", "x.M", 34, "k8s.io/kubernetes/locals.(T).M"}})
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/locals.varLiteral"),
		[]edgeRow{{"exact", "", "concrete_method", "x.M", 39, "k8s.io/kubernetes/locals.(T).M"}})
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/locals.composite"),
		[]edgeRow{{"exact", "", "concrete_method", "x.M", 44, "k8s.io/kubernetes/locals.(T).M"}})
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/locals.address"),
		[]edgeRow{{"exact", "", "concrete_method", "x.M", 49, "k8s.io/kubernetes/locals.(T).M"}})
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/locals.constructor"),
		[]edgeRow{
			{"exact", "", "func", "NewT", 53, "k8s.io/kubernetes/locals.NewT"},
			{"exact", "", "concrete_method", "x.M", 54, "k8s.io/kubernetes/locals.(T).M"},
		})
	// NewT2 has two results, so x stays unknown and x.M is a Rule 5 name_only.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/locals.constructorTwo"),
		[]edgeRow{
			{"exact", "", "func", "NewT2", 58, "k8s.io/kubernetes/locals.NewT2"},
			{"name_only", "", "unknown", "x.M", 59, "k8s.io/kubernetes/locals.(T).M"},
		})
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/locals.fieldChain"),
		[]edgeRow{{"exact", "", "concrete_method", "s.cache.Assume", 63, "k8s.io/kubernetes/locals.(*Cache).Assume"}})
	// Promoted from the embedded Inner, so the receiver is unknown and Rule 5
	// stores the one same-package Promoted method as name_only.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/locals.embedded"),
		[]edgeRow{{"name_only", "", "unknown", "o.Promoted", 67, "k8s.io/kubernetes/locals.(Inner).Promoted"}})
	// A redeclared local is unknown; Rule 5 stores the one M method.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/locals.shadowed"),
		[]edgeRow{{"name_only", "", "unknown", "x.M", 74, "k8s.io/kubernetes/locals.(T).M"}})
	// The inner a.B is Rule 3; the outer chain has an unknown receiver and Rule 5
	// finds the one C method.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/locals.chain"),
		[]edgeRow{
			{"exact", "", "concrete_method", "a.B", 79, "k8s.io/kubernetes/locals.(*Builder).B"},
			{"name_only", "", "unknown", "a.B().C", 79, "k8s.io/kubernetes/locals.(*Builder).C"},
		})

	for _, line := range []string{
		"resolution exact: 9",
		"resolution name_only: 4",
		"resolution ambiguous: 0",
		"resolution unresolved: 0",
		"call kind func: 2",
		"call kind concrete_method: 7",
		"call kind unknown: 4",
		"invariant: call sites 13 = stored 13 + skipped 0 [ok]",
	} {
		if !bytes.Contains([]byte(out), []byte(line)) {
			t.Errorf("stats output missing %q\n%s", line, out)
		}
	}
}

// TestGraphStageRule5Goldens covers Rule 5 (spec story 56) with a controlled
// package: one same-package method named Only gives a name_only row with a
// callee, two named Two give two ambiguous rows with identical raw text, and no
// method named Missing gives one unresolved row with reason unknown_receiver.
func TestGraphStageRule5Goldens(t *testing.T) {
	pool, root := graphFixture(t)
	out := runGraphStage(t, pool, root, graphOptions{Package: "k8s.io/kubernetes/rule5", Stats: true})

	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/rule5.one"),
		[]edgeRow{{"name_only", "", "unknown", "y.Only", 16, "k8s.io/kubernetes/rule5.(A).Only"}})
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/rule5.two"),
		[]edgeRow{
			{"ambiguous", "", "unknown", "y.Two", 20, "k8s.io/kubernetes/rule5.(B).Two"},
			{"ambiguous", "", "unknown", "y.Two", 20, "k8s.io/kubernetes/rule5.(C).Two"},
		})
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/rule5.none"),
		[]edgeRow{{"unresolved", "unknown_receiver", "unknown", "y.Missing", 24, ""}})

	for _, line := range []string{
		"resolution exact: 0",
		"resolution name_only: 1",
		"resolution ambiguous: 2",
		"resolution unresolved: 1",
		"reason unknown_receiver: 1",
		"call kind unknown: 4",
		"invariant: call sites 4 = stored 4 + skipped 0 [ok]",
	} {
		if !bytes.Contains([]byte(out), []byte(line)) {
			t.Errorf("stats output missing %q\n%s", line, out)
		}
	}
}

// TestGraphStageInterfaceRule4Rows covers the production Rule 4 path (cap 50):
// A (value Get, pointer Set) is the only Store candidate, the same-package
// embedded Both expands to C and D, the cross-package UsesExt contributes only
// Write (C and D) and increments interfaces_partial, Cap3's three implementers
// give three identical-raw ambiguous rows, Cap4's four stay under the cap, NoImpl
// has a known method but no indexed implementer (one no_match row), and Empty has
// no known method so it falls through to Rule 5.
func TestGraphStageInterfaceRule4Rows(t *testing.T) {
	pool, root := graphFixture(t)
	out := runGraphStage(t, pool, root, graphOptions{Package: "k8s.io/kubernetes/interfaces", Stats: true})

	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/interfaces.callStore"),
		[]edgeRow{
			{"ambiguous", "", "interface_method", "s.Get", 96, "k8s.io/kubernetes/interfaces.(A).Get"},
			{"ambiguous", "", "interface_method", "s.Set", 97, "k8s.io/kubernetes/interfaces.(*A).Set"},
		})
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/interfaces.callBoth"),
		[]edgeRow{
			{"ambiguous", "", "interface_method", "b.Write", 101, "k8s.io/kubernetes/interfaces.(C).Write"},
			{"ambiguous", "", "interface_method", "b.Write", 101, "k8s.io/kubernetes/interfaces.(D).Write"},
		})
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/interfaces.callUsesExt"),
		[]edgeRow{
			{"ambiguous", "", "interface_method", "u.Write", 105, "k8s.io/kubernetes/interfaces.(C).Write"},
			{"ambiguous", "", "interface_method", "u.Write", 105, "k8s.io/kubernetes/interfaces.(D).Write"},
		})
	// Three implementers, three ambiguous rows with identical raw text.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/interfaces.callCap3"),
		[]edgeRow{
			{"ambiguous", "", "interface_method", "c.Do3", 109, "k8s.io/kubernetes/interfaces.(Q1).Do3"},
			{"ambiguous", "", "interface_method", "c.Do3", 109, "k8s.io/kubernetes/interfaces.(Q2).Do3"},
			{"ambiguous", "", "interface_method", "c.Do3", 109, "k8s.io/kubernetes/interfaces.(Q3).Do3"},
		})
	// Four implementers, still at most the production cap of 50.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/interfaces.callCap4"),
		[]edgeRow{
			{"ambiguous", "", "interface_method", "c.Do4", 113, "k8s.io/kubernetes/interfaces.(R1).Do4"},
			{"ambiguous", "", "interface_method", "c.Do4", 113, "k8s.io/kubernetes/interfaces.(R2).Do4"},
			{"ambiguous", "", "interface_method", "c.Do4", 113, "k8s.io/kubernetes/interfaces.(R3).Do4"},
			{"ambiguous", "", "interface_method", "c.Do4", 113, "k8s.io/kubernetes/interfaces.(R4).Do4"},
		})
	// No indexed implementer: one unresolved no_match row with a NULL callee.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/interfaces.callNoImpl"),
		[]edgeRow{{"unresolved", "no_match", "interface_method", "n.Missing", 117, ""}})
	// Empty has no known method, so Rule 5 finds both same-package Get methods.
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/interfaces.callEmpty"),
		[]edgeRow{
			{"ambiguous", "", "unknown", "e.Get", 123, "k8s.io/kubernetes/interfaces.(A).Get"},
			{"ambiguous", "", "unknown", "e.Get", 123, "k8s.io/kubernetes/interfaces.(B).Get"},
		})

	for _, line := range []string{
		"resolution ambiguous: 15",
		"resolution unresolved: 1",
		"reason no_match: 1",
		"call kind interface_method: 14",
		"call kind unknown: 2",
		"interfaces_partial: 1",
		"invariant: call sites 16 = stored 16 + skipped 0 [ok]",
	} {
		if !bytes.Contains([]byte(out), []byte(line)) {
			t.Errorf("stats output missing %q\n%s", line, out)
		}
	}
	if strings.Contains(out, "deferred_interface") {
		t.Errorf("--stats still prints the removed deferred_interface counter:\n%s", out)
	}
}

// TestGraphStageInterfaceCap covers the D-075 cap with a small test override
// (Rule4N 3): Cap3's exactly three implementers give three ambiguous rows, and
// Cap4's four give one candidates_capped unresolved row with a NULL callee and
// the raw text kept.
func TestGraphStageInterfaceCap(t *testing.T) {
	pool, root := graphFixture(t)
	out := runGraphStage(t, pool, root, graphOptions{
		Package: "k8s.io/kubernetes/interfaces", Rule4N: 3, Stats: true,
	})

	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/interfaces.callCap3"),
		[]edgeRow{
			{"ambiguous", "", "interface_method", "c.Do3", 109, "k8s.io/kubernetes/interfaces.(Q1).Do3"},
			{"ambiguous", "", "interface_method", "c.Do3", 109, "k8s.io/kubernetes/interfaces.(Q2).Do3"},
			{"ambiguous", "", "interface_method", "c.Do3", 109, "k8s.io/kubernetes/interfaces.(Q3).Do3"},
		})
	assertEdges(t, edgesFor(t, pool, "k8s.io/kubernetes/interfaces.callCap4"),
		[]edgeRow{{"unresolved", "candidates_capped", "interface_method", "c.Do4", 113, ""}})

	for _, line := range []string{
		"resolution ambiguous: 11",
		"resolution unresolved: 2",
		"reason candidates_capped: 1",
		"reason no_match: 1",
		"call kind interface_method: 11",
		"interfaces_partial: 1",
		"invariant: call sites 13 = stored 13 + skipped 0 [ok]",
	} {
		if !bytes.Contains([]byte(out), []byte(line)) {
			t.Errorf("stats output missing %q\n%s", line, out)
		}
	}
}

// TestGraphStageUncappedHistogramMatchesUncappedRun proves the histogram dry run
// uses the real code path, writes nothing and its candidate total equals the
// ambiguous interface_method rows a production stored run produces (the fixture
// candidate counts stay under the production cap).
func TestGraphStageUncappedHistogramMatchesUncappedRun(t *testing.T) {
	histPool, root := graphFixture(t)
	runGraphStage(t, histPool, root, graphOptions{})
	before := scalarInt(t, histPool, `SELECT count(*) FROM edges WHERE source = 'treesitter'`)

	h := graph.NewCapHistogram()
	out := runGraphStage(t, histPool, root, graphOptions{Histogram: h, Stats: true})
	after := scalarInt(t, histPool, `SELECT count(*) FROM edges WHERE source = 'treesitter'`)
	if after != before {
		t.Errorf("histogram dry run changed the edge count %d -> %d", before, after)
	}
	if h.CandidateTotal() == 0 {
		t.Fatalf("histogram recorded no candidates:\n%s", out)
	}
	if !bytes.Contains([]byte(out), []byte("chosen N: 5")) {
		t.Errorf("histogram output missing chosen N 5:\n%s", out)
	}
	if !bytes.Contains([]byte(out), []byte("uncapped histogram dry run:")) {
		t.Errorf("histogram output should label the dry run honestly:\n%s", out)
	}
	if bytes.Contains([]byte(out), []byte("edges\n")) {
		t.Errorf("histogram output should not call the uncapped total an edge count:\n%s", out)
	}

	uncapped := testdb.New(t)
	seedFiles(t, uncapped, root)
	runParseCorpus(t, uncapped, root, 0)
	runGraphStage(t, uncapped, root, graphOptions{})
	stored := scalarInt(t, uncapped,
		`SELECT count(*) FROM edges WHERE source = 'treesitter' AND call_kind = 'interface_method' AND resolution = 'ambiguous'`)
	if stored != int64(h.CandidateTotal()) {
		t.Errorf("stored ambiguous interface rows = %d, histogram candidate total = %d",
			stored, h.CandidateTotal())
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
	if n := scalarInt(t, pool, `SELECT count(*) FROM edges WHERE source = 'treesitter'`); n != 83 {
		t.Errorf("tree-sitter edge count = %d, want 83", n)
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
	if !strings.Contains(before, "16 files stale") || !strings.Contains(before, "never graphed: 16") {
		t.Errorf("status before graphing should list 16 never-graphed files:\n%s", before)
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
