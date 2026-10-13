package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/walk"
)

// seedFixtureSymbols walks root and parses it into the files and symbols tables
// the way `indexer walk` then `indexer parse` do, so storeGT maps against the
// same symbol index tree-sitter would produce.
func seedFixtureSymbols(t *testing.T, pool *pgxpool.Pool, root string) {
	t.Helper()
	ctx := context.Background()

	files, _, err := walk.Walk(root)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if _, _, err := walk.Sync(ctx, pool, files); err != nil {
		t.Fatalf("walk sync: %v", err)
	}

	rows, err := pool.Query(ctx, `SELECT id, path, package FROM files ORDER BY package, path`)
	if err != nil {
		t.Fatalf("list files: %v", err)
	}
	type fileRow struct {
		id   int64
		path string
		pkg  string
	}
	var all []fileRow
	for rows.Next() {
		var f fileRow
		if err := rows.Scan(&f.id, &f.path, &f.pkg); err != nil {
			t.Fatalf("scan file: %v", err)
		}
		all = append(all, f)
	}
	rows.Close()

	byPkg := map[string][]fileRow{}
	var order []string
	for _, f := range all {
		if _, ok := byPkg[f.pkg]; !ok {
			order = append(order, f.pkg)
		}
		byPkg[f.pkg] = append(byPkg[f.pkg], f)
	}

	for _, pkg := range order {
		group := byPkg[pkg]
		fsyms := make([]parse.FileSymbols, 0, len(group))
		ids := make([]int64, 0, len(group))
		for _, f := range group {
			src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.path)))
			if err != nil {
				t.Fatalf("read %s: %v", f.path, err)
			}
			syms, perr := parse.File(f.path, src)
			if perr != nil && !errors.Is(perr, parse.ErrSyntax) {
				t.Fatalf("parse %s: %v", f.path, perr)
			}
			ids = append(ids, f.id)
			fsyms = append(fsyms, parse.FileSymbols{Path: f.path, Symbols: syms})
		}
		parse.AssignQualifiedNames(fsyms)
		for i := range fsyms {
			for _, s := range fsyms[i].Symbols {
				if _, err := pool.Exec(ctx, `
					INSERT INTO symbols (file_id, kind, name, qualified_name, receiver, signature, start_line, end_line)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
					ids[i], s.Kind, s.Name, s.QualifiedName, nullIfEmpty(s.Receiver),
					s.Signature, s.StartLine, s.EndLine); err != nil {
					t.Fatalf("insert symbol %s: %v", s.QualifiedName, err)
				}
			}
		}
	}
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// scalarInt runs a query returning one integer.
func scalarInt(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return n
}

// symbolID returns the id of the symbol with the given qualified name.
func symbolID(t *testing.T, pool *pgxpool.Pool, qn string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM symbols WHERE qualified_name = $1`, qn).Scan(&id); err != nil {
		t.Fatalf("symbol %s: %v", qn, err)
	}
	return id
}

// TestStoreGTFixture is Seam 2: over the committed fixture it stores rows of
// all four call kinds, attributes a closure to its enclosing function, counts
// dropped synthetic callers, prints gt_unmapped, sums the GT partition and
// gives identical counts on a second run.
func TestStoreGTFixture(t *testing.T) {
	pool := testdb.New(t)
	root, err := filepath.Abs("testdata/module")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	seedFixtureSymbols(t, pool, root)

	st, err := storeGT(context.Background(), pool, root, os.Environ())
	if err != nil {
		t.Fatalf("storeGT: %v", err)
	}
	if err := st.check(); err != nil {
		t.Fatalf("partition: %v", err)
	}

	for _, kind := range []string{"func", "concrete_method", "interface_method", "func_value"} {
		if st.ByKind[kind] == 0 {
			t.Errorf("no stored row of kind %s: %v", kind, st.ByKind)
		}
	}

	dropped := 0
	for _, n := range st.Dropped {
		dropped += n
	}
	if dropped == 0 {
		t.Error("no synthetic caller was dropped; the fixture package variable initialiser should be")
	}
	if st.Unmapped != 0 {
		t.Errorf("gt_unmapped = %d, want 0: every fixture edge should map", st.Unmapped)
	}
	if st.Total != st.Mapped+dropped+st.Unmapped {
		t.Errorf("partition %d != %d + %d + %d", st.Total, st.Mapped, dropped, st.Unmapped)
	}

	// Closure attribution: a call inside a function literal is attributed to the
	// enclosing declared function.
	closure := symbolID(t, pool, "k8s.io/kubernetes/pkg/scheduler.Closure")
	helper := symbolID(t, pool, "k8s.io/kubernetes/pkg/scheduler.helper")
	if n := scalarInt(t, pool,
		`SELECT count(*) FROM edges WHERE source = 'gopackages' AND caller_id = $1 AND callee_id = $2`,
		closure, helper); n != 1 {
		t.Errorf("edges Closure -> helper = %d, want 1", n)
	}

	// A generic pointer-receiver method maps: its receiver spelling preserves the
	// type parameter, matching internal/parse.
	if n := scalarInt(t, pool,
		`SELECT count(*) FROM edges WHERE source = 'gopackages' AND callee_raw = $1`,
		"k8s.io/kubernetes/pkg/scheduler.(*Heap[T]).AddOrUpdate"); n == 0 {
		t.Error("generic method (*Heap[T]).AddOrUpdate did not map to a symbol")
	}
	// A plain pointer-receiver method maps too (story 15).
	if n := scalarInt(t, pool,
		`SELECT count(*) FROM edges WHERE source = 'gopackages' AND callee_raw = $1`,
		"k8s.io/kubernetes/pkg/scheduler.(*counter).Inc"); n == 0 {
		t.Error("pointer-receiver method (*counter).Inc did not map to a symbol")
	}

	// Every stored row is exact, has no reason and a non-null callee (D-077).
	if n := scalarInt(t, pool,
		`SELECT count(*) FROM edges WHERE source = 'gopackages'
		   AND (resolution <> 'exact' OR unresolved_reason IS NOT NULL OR callee_id IS NULL)`); n != 0 {
		t.Errorf("%d gopackages rows are not exact with a callee", n)
	}
	if n := scalarInt(t, pool, `SELECT count(*) FROM edges WHERE source = 'gopackages'`); int(n) != st.Rows {
		t.Errorf("stored rows = %d, want %d", n, st.Rows)
	}

	var buf bytes.Buffer
	printGTStore(&buf, st)
	t.Logf("fixture gt store:\n%s", strings.TrimRight(buf.String(), "\n"))
	if !strings.Contains(buf.String(), "gt_unmapped") {
		t.Errorf("output does not print gt_unmapped:\n%s", buf.String())
	}

	// A second run gives identical counts (deterministic, replace-in-one-tx).
	second, err := storeGT(context.Background(), pool, root, os.Environ())
	if err != nil {
		t.Fatalf("second storeGT: %v", err)
	}
	if !reflect.DeepEqual(st, second) {
		t.Errorf("second run differs:\n first %+v\nsecond %+v", st, second)
	}
}
