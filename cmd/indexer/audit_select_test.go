package main

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/summarize"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
)

// The audit samples every in-slice function and method regardless of body
// length (it has no eligibility threshold), and uses the shared in-slice
// function predicate.
func TestSelectAuditRowsTakesAllInSliceFuncsAndMethods(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	fixtures := []struct {
		name    string
		kind    string
		inSlice bool
		lines   int
	}{
		{"a_func_2", "func", true, 2},
		{"b_func_4", "func", true, 4},
		{"c_method_1", "method", true, 1},
		{"d_struct", "struct", true, 4},
		{"e_func_out", "func", false, 4},
	}
	for i, f := range fixtures {
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO files (path, package, sha, loc, in_slice)
			VALUES ($1, 'p', 'sha', 100, $2) RETURNING id`, fmt.Sprintf("f%d.go", i), f.inSlice).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO symbols (file_id, kind, name, qualified_name, start_line, end_line, raw_calls)
			VALUES ($1, $2, $3, $3, 1, $4, '{}')`, id, f.kind, f.name, 1+f.lines); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := selectAuditRows(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		got = append(got, r.QualifiedName)
	}
	if want := []string{"a_func_2", "b_func_4", "c_method_1"}; !slices.Equal(got, want) {
		t.Fatalf("audit rows = %v, want %v", got, want)
	}
	if summarize.InSliceFuncWhere() == "" {
		t.Fatal("empty predicate")
	}
}
