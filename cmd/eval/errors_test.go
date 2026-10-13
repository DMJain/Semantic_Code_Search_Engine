package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/graph"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
)

// TestAllocateQuotas pins the proportional (largest-remainder) quota rule: the
// quotas sum to min(total, capacity), never exceed a stratum's size, and ties
// break by stratum order.
func TestAllocateQuotas(t *testing.T) {
	cases := []struct {
		total int
		sizes []int
		want  []int
	}{
		{3, []int{1, 1, 1}, []int{1, 1, 1}},
		{10, []int{0, 0}, []int{0, 0}},
		{5, []int{2, 2}, []int{2, 2}}, // clamped by capacity
		{30, []int{9, 5, 514, 1092, 0}, []int{0, 0, 10, 20, 0}},
		{30, []int{278, 232, 817, 385}, []int{5, 4, 14, 7}},
		{2, []int{1, 1, 1}, []int{1, 1, 0}}, // ties break by index
	}
	for _, c := range cases {
		got := allocate(c.total, c.sizes)
		if len(got) != len(c.sizes) {
			t.Fatalf("allocate(%d, %v) length = %d", c.total, c.sizes, len(got))
		}
		wantSum := 0
		for _, s := range c.sizes {
			wantSum += s
		}
		if wantSum > c.total {
			wantSum = c.total
		}
		sum := 0
		for i, q := range got {
			if q < 0 || q > c.sizes[i] {
				t.Errorf("allocate(%d, %v)[%d] = %d, out of range", c.total, c.sizes, i, q)
			}
			sum += q
		}
		if sum != wantSum {
			t.Errorf("allocate(%d, %v) sum = %d, want %d", c.total, c.sizes, sum, wantSum)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("allocate(%d, %v) = %v, want %v", c.total, c.sizes, got, c.want)
		}
	}
}

// TestEnumerateErrors reuses the scoring core: a scored unit with no matching
// ground-truth pair is a false positive; an in-slice ground-truth pair no unit
// covers is a false negative.
func TestEnumerateErrors(t *testing.T) {
	in := scoreInput{
		InSlice: set(1, 2, 3, 4, 5, 6),
		TS: []tsRow{
			ts(1, 2, "f", graph.KindFunc, 1),                  // TP
			ts(1, 3, "g", graph.KindFunc, 2),                  // FP: candidate 3 in slice, no GT
			tsUnresolved(1, "x", graph.ReasonUnknownReceiver), // unresolved: not scored
			tsAmb(1, 4, "m", 5), tsAmb(1, 6, "m", 5),          // unit {4,6}; GT (1,4) TP
		},
		GT: []gtRow{
			gt(1, 2, graph.KindFunc),
			gt(1, 4, graph.KindInterfaceMethod),
			gt(1, 5, graph.KindConcreteMethod), // no unit covers -> FN
		},
	}
	fps, fns, err := enumerateErrors(in)
	if err != nil {
		t.Fatalf("enumerateErrors: %v", err)
	}
	if len(fps) != 1 {
		t.Fatalf("fps = %d, want 1: %+v", len(fps), fps)
	}
	if fps[0].CallerID != 1 || fps[0].Kind != graph.KindFunc || len(fps[0].CandIDs) != 1 || fps[0].CandIDs[0] != 3 {
		t.Errorf("fp = %+v, want caller 1 kind func candidate 3", fps[0])
	}
	if len(fns) != 1 {
		t.Fatalf("fns = %d, want 1: %+v", len(fns), fns)
	}
	if fns[0].CallerID != 1 || fns[0].CalleeID != 5 || fns[0].Kind != graph.KindConcreteMethod {
		t.Errorf("fn = %+v, want (1,5) concrete_method", fns[0])
	}
}

// syntheticErrors builds a scoreInput with n FPs and n FNs, alternating kinds so
// the stratification has something to spread across.
func syntheticErrors(n int) scoreInput {
	in := scoreInput{InSlice: map[int64]bool{}}
	kinds := []string{graph.KindFunc, graph.KindConcreteMethod, graph.KindInterfaceMethod, graph.KindUnknown}
	// FP pairs: caller 1 -> callee i with no GT.
	for i := 0; i < n; i++ {
		callee := int64(1000 + i)
		in.InSlice[1] = true
		in.InSlice[callee] = true
		in.TS = append(in.TS, ts(1, callee, "c", kinds[i%len(kinds)], i+1))
	}
	// FN pairs: caller 2 -> callee j with no TS.
	for j := 0; j < n; j++ {
		callee := int64(2000 + j)
		in.InSlice[2] = true
		in.InSlice[callee] = true
		in.GT = append(in.GT, gt(2, callee, kinds[j%len(kinds)]))
	}
	return in
}

// TestSampleErrorsDeterministicAndStratified: the draw is stable for a given
// input, keeps each stratum within its quota, and totals 30 per class when
// enough errors exist.
func TestSampleErrorsDeterministicAndStratified(t *testing.T) {
	in := syntheticErrors(60)
	fps, fns, err := enumerateErrors(in)
	if err != nil {
		t.Fatalf("enumerateErrors: %v", err)
	}
	fpSel, fnSel, fpQuota, fnQuota := sampleErrors(fps, fns)

	if len(fpSel) != 30 || len(fnSel) != 30 {
		t.Fatalf("selected fp/fn = %d/%d, want 30/30", len(fpSel), len(fnSel))
	}
	assertWithinQuota(t, fpSel, fpQuota, func(u fpUnit) string { return u.Kind })
	assertWithinQuota(t, fnSel, fnQuota, func(p fnPair) string { return p.Kind })

	fpSel2, fnSel2, _, _ := sampleErrors(fps, fns)
	if !reflect.DeepEqual(fpSel, fpSel2) || !reflect.DeepEqual(fnSel, fnSel2) {
		t.Errorf("sampling is not deterministic")
	}

	// A chosen item is one of the inputs.
	inSet := map[int64]bool{}
	for _, u := range fps {
		inSet[u.CallerID*1000000+int64(u.Line)] = true
	}
	for _, u := range fpSel {
		if !inSet[u.CallerID*1000000+int64(u.Line)] {
			t.Errorf("selected fp not in input: %+v", u)
		}
	}
}

func assertWithinQuota[T any](t *testing.T, sel []T, quota map[string]int, kind func(T) string) {
	t.Helper()
	counts := map[string]int{}
	for _, s := range sel {
		counts[kind(s)]++
	}
	for k, n := range counts {
		if n > quota[k] {
			t.Errorf("kind %s count %d exceeds quota %d", k, n, quota[k])
		}
	}
}

// TestSampleErrorsClampsWhenSparse: with fewer errors than the target, all are
// returned.
func TestSampleErrorsClampsWhenSparse(t *testing.T) {
	in := syntheticErrors(4)
	fps, fns, err := enumerateErrors(in)
	if err != nil {
		t.Fatalf("enumerateErrors: %v", err)
	}
	fpSel, fnSel, _, _ := sampleErrors(fps, fns)
	if len(fpSel) != len(fps) || len(fnSel) != len(fns) {
		t.Errorf("sparse selection fp/fn = %d/%d, want %d/%d", len(fpSel), len(fnSel), len(fps), len(fns))
	}
}

// TestBuildErrorSampleNonVacuous: the rendered sample has 30 FPs and 30 FNs,
// each with a caller, a file and a source line.
func TestBuildErrorSampleNonVacuous(t *testing.T) {
	in := syntheticErrors(60)
	deps := sampleDeps{
		Symbol: func(_ int64) symDetail {
			return symDetail{Qualified: "pkg.Sym", File: "pkg/f.go", StartLine: 1, EndLine: 3}
		},
		Source: func(_ string, _ int) string { return "x.Call()" },
	}
	doc, err := buildErrorSample(in, deps)
	if err != nil {
		t.Fatalf("buildErrorSample: %v", err)
	}
	if len(doc.Samples) != 60 {
		t.Fatalf("samples = %d, want 60", len(doc.Samples))
	}
	fp, fn := 0, 0
	for _, s := range doc.Samples {
		switch s.Type {
		case "fp":
			fp++
		case "fn":
			fn++
		default:
			t.Errorf("sample type = %q, want fp or fn", s.Type)
		}
		if s.Caller == "" || s.File == "" || s.Source == "" {
			t.Errorf("vacuous sample: %+v", s)
		}
		if s.Kind == "" {
			t.Errorf("sample missing kind: %+v", s)
		}
	}
	if fp != 30 || fn != 30 {
		t.Errorf("fp/fn = %d/%d, want 30/30", fp, fn)
	}
	if doc.FPTotal != 60 || doc.FNTotal != 60 {
		t.Errorf("documented totals = %d/%d, want 60/60", doc.FPTotal, doc.FNTotal)
	}
}

// TestSampleCallgraphEndToEnd wires the DB queries, the symbol lookup and the
// source reader over a throwaway database: one false positive (a resolved unit
// with no ground truth) and one false negative (a ground-truth pair no unit
// covers) are rendered with their caller and source line.
func TestSampleCallgraphEndToEnd(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, root, "p/a.go", "package x\n\nfunc A() { B(); C() }\n\nfunc B() {}\n\nfunc C() {}\n")

	var fileID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO files (path, package, sha, loc, in_slice) VALUES ('p/a.go', 'x', 'sha1', 7, true) RETURNING id`).Scan(&fileID); err != nil {
		t.Fatalf("insert file: %v", err)
	}
	var caller, callee, missing int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO symbols (file_id, kind, name, qualified_name, start_line, end_line)
		 VALUES ($1, 'func', 'A', 'x.A', 3, 3) RETURNING id`, fileID).Scan(&caller); err != nil {
		t.Fatalf("insert caller: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO symbols (file_id, kind, name, qualified_name, start_line, end_line)
		 VALUES ($1, 'func', 'B', 'x.B', 5, 5) RETURNING id`, fileID).Scan(&callee); err != nil {
		t.Fatalf("insert callee: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO symbols (file_id, kind, name, qualified_name, start_line, end_line)
		 VALUES ($1, 'func', 'C', 'x.C', 7, 7) RETURNING id`, fileID).Scan(&missing); err != nil {
		t.Fatalf("insert missing: %v", err)
	}
	// A -> B is resolved but has no ground truth: a false positive.
	if _, err := pool.Exec(ctx,
		`INSERT INTO edges (source, caller_id, callee_id, callee_raw, resolution, call_kind, call_line)
		 VALUES ('treesitter', $1, $2, 'B', 'exact', 'func', 3)`, caller, callee); err != nil {
		t.Fatalf("insert ts edge: %v", err)
	}
	// A -> C is ground truth no tree-sitter unit covers: a false negative.
	if _, err := pool.Exec(ctx,
		`INSERT INTO edges (source, caller_id, callee_id, callee_raw, resolution, call_kind, call_line)
		 VALUES ('gopackages', $1, $2, 'x.C', 'exact', 'func', 3)`, caller, missing); err != nil {
		t.Fatalf("insert gt edge: %v", err)
	}

	symbols, err := graph.LoadSymbols(ctx, pool)
	if err != nil {
		t.Fatalf("LoadSymbols: %v", err)
	}
	names, err := graph.BuildNameIndex(ctx, pool, root)
	if err != nil {
		t.Fatalf("BuildNameIndex: %v", err)
	}
	fp := graph.Fingerprint(symbols, names, graph.ResolverVersion)
	if _, err := pool.Exec(ctx, `UPDATE files SET graphed_sha = 'sha1', graphed_symbols_fp = $1`, fp); err != nil {
		t.Fatalf("mark graphed: %v", err)
	}

	out := filepath.Join(t.TempDir(), "error-sample.json")
	if err := sampleCallgraph(ctx, pool, root, out, os.Stdout); err != nil {
		t.Fatalf("sampleCallgraph: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	var doc errorSampleDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal sample: %v", err)
	}
	if doc.FPTotal != 1 || doc.FNTotal != 1 || len(doc.Samples) != 2 {
		t.Fatalf("doc = %+v, want 1 fp, 1 fn", doc)
	}
	if doc.Seed != 11 {
		t.Errorf("seed = %d, want 11", doc.Seed)
	}
	for _, s := range doc.Samples {
		if s.Caller == "" || s.File == "" || s.Source == "" {
			t.Errorf("vacuous sample: %+v", s)
		}
	}
}
