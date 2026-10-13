package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

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

// TestGraphStageRerunIdentical proves a rerun rebuilds the same rows and prints
// the same counts, and that the shape rule holds in the database.
func TestGraphStageRerunIdentical(t *testing.T) {
	pool, root := graphFixture(t)
	opts := graphOptions{Stats: true} // every file, both fixture packages

	first := runGraphStage(t, pool, root, opts)
	before := edgesFor(t, pool, "k8s.io/kubernetes/plain.caller")
	second := runGraphStage(t, pool, root, opts)
	after := edgesFor(t, pool, "k8s.io/kubernetes/plain.caller")

	if first != second {
		t.Errorf("rerun output differs:\nfirst  %s\nsecond %s", first, second)
	}
	assertEdges(t, after, before)

	if n := scalarInt(t, pool,
		`SELECT count(*) FROM edges WHERE (resolution = 'unresolved') <> (callee_id IS NULL)`); n != 0 {
		t.Errorf("rows breaking the unresolved shape = %d, want 0", n)
	}
	if n := scalarInt(t, pool, `SELECT count(*) FROM edges WHERE source = 'treesitter'`); n != 4 {
		t.Errorf("tree-sitter edge count = %d, want 4", n)
	}
}

// TestGraphStageLimitKeepsPackagesWhole proves --limit stops at package
// boundaries: a package is graphed whole, so the declared-name index covers all
// its files. A flat file limit would drop the package's second file (here the
// one declaring type Foo) and turn a conversion into a stored no_match edge.
func TestGraphStageLimitKeepsPackagesWhole(t *testing.T) {
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
		`SELECT count(*) FROM files WHERE package = 'k8s.io/kubernetes/p/a' AND graphed_sha IS NOT NULL`); n != 2 {
		t.Errorf("package a files graphed under --limit 1 = %d, want 2 (whole package)", n)
	}
	if n := scalarInt(t, limited,
		`SELECT count(*) FROM files WHERE package = 'k8s.io/kubernetes/p/b' AND graphed_sha IS NOT NULL`); n != 0 {
		t.Errorf("package b graphed under --limit 1 = %d files, want 0", n)
	}

	// Without the limit the same package's rows are produced.
	full := testdb.New(t)
	seedFiles(t, full, root)
	runParseCorpus(t, full, root, 0)
	runGraphStage(t, full, root, graphOptions{Stats: true})
	assertEdges(t, edgesFor(t, full, "k8s.io/kubernetes/p/a.use"), edgesFor(t, limited, "k8s.io/kubernetes/p/a.use"))
}
