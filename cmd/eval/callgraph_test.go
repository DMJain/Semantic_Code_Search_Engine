package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/graph"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
)

func set(ids ...int64) map[int64]bool {
	m := map[int64]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// ts builds a resolved tree-sitter row.
func ts(caller, callee int64, raw, kind string, line int) tsRow {
	return tsRow{CallerID: caller, CalleeID: callee, CalleeRaw: raw, CallKind: kind, CallLine: line}
}

// tsAmb builds an ambiguous tree-sitter row (one candidate of a call site).
func tsAmb(caller, callee int64, raw string, line int) tsRow {
	return tsRow{CallerID: caller, CalleeID: callee, CalleeRaw: raw, CallKind: graph.KindInterfaceMethod, CallLine: line}
}

// tsUnresolved builds an unresolved tree-sitter row. A function-value call is
// kind func_value; every other unresolved reason is kind unknown.
func tsUnresolved(caller int64, raw, reason string) tsRow {
	kind := graph.KindUnknown
	if reason == graph.ReasonFuncValue {
		kind = graph.KindFuncValue
	}
	return tsRow{CallerID: caller, CalleeRaw: raw, CallKind: kind, Reason: reason}
}

// val returns a defined rate or fails, so a test cannot accidentally treat a
// nil (undefined) rate as zero.
func val(t *testing.T, p *float64) float64 {
	t.Helper()
	if p == nil {
		t.Fatalf("rate is undefined (nil), want a value")
	}
	return *p
}

// ptrEq compares two optional rates by value, treating two nils as equal.
func ptrEq(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func gt(caller, callee int64, kind string) gtRow {
	return gtRow{CallerID: caller, CalleeID: callee, CallKind: kind}
}

func mustScore(t *testing.T, in scoreInput) callgraphReport {
	t.Helper()
	rep, err := score(in)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	return rep
}

// TestScorePerfect: an exact tree-sitter edge matching the ground truth scores
// precision = recall = 1, overall and by kind.
func TestScorePerfect(t *testing.T) {
	rep := mustScore(t, scoreInput{
		InSlice: set(1, 2),
		TS:      []tsRow{ts(1, 2, "f", graph.KindFunc, 3)},
		GT:      []gtRow{gt(1, 2, graph.KindFunc)},
	})
	if val(t, rep.Precision) != 1 || val(t, rep.Recall) != 1 {
		t.Errorf("precision/recall = %v/%v, want 1/1", rep.Precision, rep.Recall)
	}
	if k := rep.ByKind[graph.KindFunc]; val(t, k.Precision) != 1 || val(t, k.Recall) != 1 {
		t.Errorf("func kind precision/recall = %v/%v, want 1/1", k.Precision, k.Recall)
	}
	if rep.Counts.ScoredInSlice != 1 || rep.Counts.ResolvedOutOfSlice != 0 || rep.Counts.Unresolved != 0 {
		t.Errorf("buckets = %+v", rep.Counts)
	}
}

// TestScoreAllAmbiguous: an ambiguous call site is a TP only when the GT callee
// is among its candidates; ambiguity not matching any GT is a FP, and the
// ambiguous rate and mean candidate size are reported per kind.
func TestScoreAllAmbiguous(t *testing.T) {
	in := scoreInput{
		InSlice: set(1, 2, 3, 4, 5),
		TS: []tsRow{
			// Unit A: candidates {2,3}; GT (1,2) is among them -> TP.
			tsAmb(1, 2, "x", 1),
			tsAmb(1, 3, "x", 1),
			// Unit B: candidates {4,5}; no GT -> FP.
			tsAmb(1, 4, "y", 2),
			tsAmb(1, 5, "y", 2),
		},
		GT: []gtRow{gt(1, 2, graph.KindInterfaceMethod)},
	}
	rep := mustScore(t, in)
	if rep.Counts.PrecisionUniverse != 2 || rep.Counts.TP != 1 {
		t.Fatalf("precision universe/tp = %d/%d, want 2/1", rep.Counts.PrecisionUniverse, rep.Counts.TP)
	}
	if want := 0.5; val(t, rep.Precision) != want {
		t.Errorf("precision = %v, want %v", rep.Precision, want)
	}
	if val(t, rep.Recall) != 1 {
		t.Errorf("recall = %v, want 1 (recall counts an ambiguous candidate as coverage)", rep.Recall)
	}
	k := rep.ByKind[graph.KindInterfaceMethod]
	if val(t, k.AmbiguousRate) != 1 {
		t.Errorf("ambiguous rate = %v, want 1", k.AmbiguousRate)
	}
	if val(t, k.MeanCandidates) != 2 {
		t.Errorf("mean candidates = %v, want 2", k.MeanCandidates)
	}
}

// TestScoreMissing: a GT pair no tree-sitter edge covers is a false negative.
func TestScoreMissing(t *testing.T) {
	rep := mustScore(t, scoreInput{
		InSlice: set(1, 2, 3),
		TS:      []tsRow{ts(1, 2, "f", graph.KindFunc, 1)},
		GT:      []gtRow{gt(1, 2, graph.KindFunc), gt(1, 3, graph.KindFunc)},
	})
	if val(t, rep.Recall) != 0.5 {
		t.Errorf("recall = %v, want 0.5", rep.Recall)
	}
	if rep.Counts.RecallUniverse != 2 || rep.Counts.Found != 1 || rep.Counts.FN != 1 {
		t.Errorf("recall counts = %+v", rep.Counts)
	}
}

// TestScoreOutOfSliceShare: an edge resolved to an out-of-slice callee is in its
// own bucket, is neither TP nor FP, and its share is reported.
func TestScoreOutOfSliceShare(t *testing.T) {
	in := scoreInput{
		InSlice: set(1, 2), // 9 is out of slice
		TS: []tsRow{
			ts(1, 2, "in", graph.KindFunc, 1),
			ts(1, 9, "out", graph.KindFunc, 2),
		},
		GT: []gtRow{gt(1, 2, graph.KindFunc)},
	}
	rep := mustScore(t, in)
	if rep.Counts.ScoredInSlice != 1 || rep.Counts.ResolvedOutOfSlice != 1 {
		t.Errorf("buckets = %+v", rep.Counts)
	}
	if val(t, rep.Counts.OutOfSliceShare) != 0.5 {
		t.Errorf("out-of-slice share = %v, want 0.5", rep.Counts.OutOfSliceShare)
	}
	// The out-of-slice unit is excluded from the precision universe.
	if rep.Counts.PrecisionUniverse != 1 {
		t.Errorf("precision universe = %d, want 1", rep.Counts.PrecisionUniverse)
	}
	if val(t, rep.Precision) != 1 {
		t.Errorf("precision = %v, want 1", rep.Precision)
	}
	if !strings.Contains(rep.Note, "in-slice only") || !strings.Contains(rep.Note, "out-of-slice share") {
		t.Errorf("note does not state the scope and share: %q", rep.Note)
	}
}

// TestScoreUnresolvedByReason: every unresolved reason lands in its own bucket
// and the buckets sum to the in-slice-caller total (story 98).
func TestScoreUnresolvedByReason(t *testing.T) {
	reasons := []string{
		graph.ReasonExternal, graph.ReasonNoMatch, graph.ReasonUnknownReceiver,
		graph.ReasonFuncValue, graph.ReasonCandidatesCap,
	}
	var rows []tsRow
	for _, r := range reasons {
		rows = append(rows, tsUnresolved(1, r, r))
	}
	rep := mustScore(t, scoreInput{InSlice: set(1), TS: rows, GT: nil})
	if rep.Counts.Unresolved != len(reasons) {
		t.Fatalf("unresolved = %d, want %d", rep.Counts.Unresolved, len(reasons))
	}
	for _, r := range reasons {
		if rep.Counts.UnresolvedByReason[r] != 1 {
			t.Errorf("reason %s = %d, want 1", r, rep.Counts.UnresolvedByReason[r])
		}
	}
	sum := rep.Counts.ScoredInSlice + rep.Counts.ResolvedOutOfSlice + rep.Counts.Unresolved
	if sum != rep.Counts.TSEdges {
		t.Errorf("buckets sum %d != total %d", sum, rep.Counts.TSEdges)
	}
}

// TestGTPartitionAssertion: the GT partition must sum to its total and match the
// stored rows, or the report refuses.
func TestGTPartitionAssertion(t *testing.T) {
	rep := mustScore(t, scoreInput{
		InSlice: set(1, 2),
		TS:      []tsRow{ts(1, 2, "f", graph.KindFunc, 1)},
		GT:      []gtRow{gt(1, 2, graph.KindFunc)},
	})
	good := gtPartition{Total: 10, Mapped: 7, Unmapped: 1, Rows: 1,
		Dropped: map[string]int{"init": 2}, ByKind: map[string]int{"func": 1}}
	if err := rep.assertGTPartition(good); err != nil {
		t.Fatalf("valid partition refused: %v", err)
	}
	if rep.Counts.GTTotal != 10 || rep.Counts.GTMapped != 7 || rep.Counts.GTUnmapped != 1 {
		t.Errorf("partition not recorded: %+v", rep.Counts)
	}
	if rep.Counts.GTByKind["func"] != 1 {
		t.Errorf("gt by kind not recorded: %+v", rep.Counts.GTByKind)
	}

	bad := gtPartition{Total: 10, Mapped: 7, Unmapped: 1, Rows: 1,
		Dropped: map[string]int{"init": 1}, ByKind: map[string]int{"func": 1}}
	if err := rep.assertGTPartition(bad); err == nil {
		t.Error("partition that does not sum was accepted")
	}
	rows := gtPartition{Total: 1, Mapped: 1, Unmapped: 0, Rows: 99,
		Dropped: map[string]int{}, ByKind: map[string]int{"func": 1}}
	if err := rep.assertGTPartition(rows); err == nil {
		t.Error("rows mismatch was accepted")
	}
	kinds := gtPartition{Total: 2, Mapped: 2, Unmapped: 0, Rows: 1,
		Dropped: map[string]int{}, ByKind: map[string]int{"func": 1, "concrete_method": 1}}
	if err := rep.assertGTPartition(kinds); err == nil {
		t.Error("by-kind mismatch was accepted")
	}
}

// TestMultiLineAndSameLineDuplicateScoreIdentically proves line is not a
// scoring input: one call at two lines scores the same as two rows collapsed to
// one call site on a single line (story 86).
func TestMultiLineAndSameLineDuplicateScoreIdentically(t *testing.T) {
	gtRows := []gtRow{gt(1, 2, graph.KindFunc)}
	duplicate := mustScore(t, scoreInput{
		InSlice: set(1, 2),
		TS: []tsRow{
			ts(1, 2, "f", graph.KindFunc, 5),
			ts(1, 2, "f", graph.KindFunc, 5),
		},
		GT: gtRows,
	})
	multiLine := mustScore(t, scoreInput{
		InSlice: set(1, 2),
		TS: []tsRow{
			ts(1, 2, "f", graph.KindFunc, 5),
			ts(1, 2, "f", graph.KindFunc, 9),
		},
		GT: gtRows,
	})
	if !ptrEq(duplicate.Precision, multiLine.Precision) || !ptrEq(duplicate.Recall, multiLine.Recall) {
		t.Errorf("duplicate %v/%v != multi-line %v/%v", duplicate.Precision, duplicate.Recall, multiLine.Precision, multiLine.Recall)
	}
	if val(t, duplicate.Precision) != 1 || val(t, duplicate.Recall) != 1 {
		t.Errorf("duplicate precision/recall = %v/%v, want 1/1", duplicate.Precision, duplicate.Recall)
	}
	// Same-line duplicates collapse to one unit; the multi-line pair is two
	// units but both are TP, so the precision universe differs while the rate
	// does not.
	if duplicate.ByKind[graph.KindFunc].ScoredUnits != 1 {
		t.Errorf("same-line duplicate units = %d, want 1", duplicate.ByKind[graph.KindFunc].ScoredUnits)
	}
	if multiLine.ByKind[graph.KindFunc].ScoredUnits != 2 {
		t.Errorf("multi-line units = %d, want 2", multiLine.ByKind[graph.KindFunc].ScoredUnits)
	}
	if multiLine.Counts.PrecisionUniverse != 2 {
		t.Errorf("multi-line precision universe = %d, want 2", multiLine.Counts.PrecisionUniverse)
	}
}

// goldenFixture exercises every kind and the ambiguity path, and is the input
// for the JSON and markdown goldens.
func goldenFixture() scoreInput {
	return scoreInput{
		InSlice: set(1, 2, 3, 4, 5, 6),
		TS: []tsRow{
			ts(1, 2, "pkg.F", graph.KindFunc, 1),
			ts(1, 3, "recv.M", graph.KindConcreteMethod, 2),
			tsAmb(1, 4, "iface.Do", 3),
			tsAmb(1, 5, "iface.Do", 3),
			tsUnresolved(1, "missing", graph.ReasonUnknownReceiver),
			tsUnresolved(1, "fn", graph.ReasonFuncValue),
		},
		GT: []gtRow{
			gt(1, 2, graph.KindFunc),
			gt(1, 4, graph.KindInterfaceMethod),
			gt(1, 6, graph.KindFuncValue),
		},
	}
}

// goldenReport scores the fixture and records a GT partition, so the goldens
// have the same shape the report writes (partition counts included).
func goldenReport(t *testing.T) callgraphReport {
	t.Helper()
	rep := mustScore(t, goldenFixture())
	part := gtPartition{
		Total: 6, Mapped: 3, Unmapped: 0, Rows: 3,
		Dropped: map[string]int{"init": 2, "wrapper": 1},
		ByKind:  map[string]int{"func": 1, "interface_method": 1, "func_value": 1},
	}
	if err := rep.assertGTPartition(part); err != nil {
		t.Fatalf("assertGTPartition: %v", err)
	}
	return rep
}

func TestScoreGoldenJSON(t *testing.T) {
	rep := goldenReport(t)
	got, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	compareGolden(t, "callgraph_result.json.golden", append(got, '\n'))
}

func TestScoreGoldenMarkdown(t *testing.T) {
	rep := goldenReport(t)
	compareGolden(t, "callgraph_table.md.golden", []byte(rep.markdown()))
}

func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run UPDATE_GOLDEN=1 to create): %v", name, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("golden %s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// TestStaleFileCountRefusesAndClears is the refusal path: a file with no
// graphed fingerprint is stale; after it is graphed at the current fingerprint
// the count is zero.
func TestStaleFileCountRefusesAndClears(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, root, "p/a.go", "package x\n\nfunc A() {}\n")

	if _, err := pool.Exec(ctx,
		`INSERT INTO files (path, package, sha, loc, in_slice) VALUES ('p/a.go', 'x', 'sha1', 3, true)`); err != nil {
		t.Fatalf("insert file: %v", err)
	}
	stale, err := staleFileCount(ctx, pool, root)
	if err != nil {
		t.Fatalf("staleFileCount: %v", err)
	}
	if stale != 1 {
		t.Fatalf("stale = %d, want 1 (never graphed)", stale)
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
	if _, err := pool.Exec(ctx,
		`UPDATE files SET graphed_sha = 'sha1', graphed_symbols_fp = $1 WHERE path = 'p/a.go'`, fp); err != nil {
		t.Fatalf("mark graphed: %v", err)
	}
	stale, err = staleFileCount(ctx, pool, root)
	if err != nil {
		t.Fatalf("staleFileCount: %v", err)
	}
	if stale != 0 {
		t.Fatalf("stale = %d, want 0 after graphing", stale)
	}
}

// TestReportCallgraphEndToEnd wires the DB queries, the GT partition file and
// the scoring core over a throwaway database.
func TestReportCallgraphEndToEnd(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, root, "p/a.go", "package x\n\nfunc A() { B() }\n\nfunc B() {}\n")

	var fileID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO files (path, package, sha, loc, in_slice) VALUES ('p/a.go', 'x', 'sha1', 5, true) RETURNING id`).Scan(&fileID); err != nil {
		t.Fatalf("insert file: %v", err)
	}
	var caller, callee int64
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
	if _, err := pool.Exec(ctx,
		`INSERT INTO edges (source, caller_id, callee_id, callee_raw, resolution, call_kind, call_line)
		 VALUES ('treesitter', $1, $2, 'B', 'exact', 'func', 3)`, caller, callee); err != nil {
		t.Fatalf("insert ts edge: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO edges (source, caller_id, callee_id, callee_raw, resolution, call_kind, call_line)
		 VALUES ('gopackages', $1, $2, 'x.B', 'exact', 'func', 0)`, caller, callee); err != nil {
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

	gtPath := filepath.Join(t.TempDir(), "gt-store.json")
	part := gtPartition{Total: 1, Mapped: 1, Unmapped: 0, Rows: 1, Dropped: map[string]int{}, ByKind: map[string]int{"func": 1}}
	data, _ := json.Marshal(part)
	if err := os.WriteFile(gtPath, data, 0o644); err != nil {
		t.Fatalf("write gt partition: %v", err)
	}

	out := filepath.Join(t.TempDir(), "result.json")
	var buf bytes.Buffer
	if err := reportCallgraph(ctx, pool, root, gtPath, out, &buf); err != nil {
		t.Fatalf("reportCallgraph: %v", err)
	}
	if !strings.Contains(buf.String(), "Call-graph precision/recall") {
		t.Errorf("stdout missing table header:\n%s", buf.String())
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	var rep callgraphReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if val(t, rep.Precision) != 1 || val(t, rep.Recall) != 1 {
		t.Errorf("precision/recall = %v/%v, want 1/1\n%s", rep.Precision, rep.Recall, raw)
	}
	if rep.Counts.GTTotal != 1 || rep.Counts.GTStored != 1 {
		t.Errorf("gt counts = %+v", rep.Counts)
	}
}

// TestReportCallgraphRefusesStale is the refusal at the report boundary: a
// never-graphed file makes reportCallgraph return an error, before it scores.
func TestReportCallgraphRefusesStale(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, root, "p/a.go", "package x\n")
	if _, err := pool.Exec(ctx,
		`INSERT INTO files (path, package, sha, loc, in_slice) VALUES ('p/a.go', 'x', 'sha1', 1, true)`); err != nil {
		t.Fatalf("insert file: %v", err)
	}
	out := filepath.Join(t.TempDir(), "result.json")
	err := reportCallgraph(ctx, pool, root, "does-not-exist.json", out, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("reportCallgraph error = %v, want a staleness refusal", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Errorf("report wrote a result despite the refusal")
	}
}

// TestUndefinedRatesAreNil proves a zero denominator yields an undefined rate
// (nil, JSON null), not 0: func_value has no scored units and the unknown kind
// has no GT pairs in the golden fixture.
func TestUndefinedRatesAreNil(t *testing.T) {
	rep := mustScore(t, goldenFixture())
	if k := rep.ByKind[graph.KindFuncValue]; k.Precision != nil {
		t.Errorf("func_value precision = %v, want nil (no scored units)", *k.Precision)
	}
	if k := rep.ByKind[graph.KindUnknown]; k.Recall != nil {
		t.Errorf("unknown recall = %v, want nil (no GT pairs)", *k.Recall)
	}
	empty := mustScore(t, scoreInput{InSlice: set(1)})
	if empty.Precision != nil || empty.Recall != nil {
		t.Errorf("empty report precision/recall = %v/%v, want nil/nil", empty.Precision, empty.Recall)
	}
	raw, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"precision":null`) {
		t.Errorf("undefined precision did not marshal as null: %s", raw)
	}
}

// TestAmbiguousUnitStraddlesSliceBoundary: a call site with one in-slice and one
// out-of-slice candidate is a scored unit (it has an in-slice candidate) and a
// TP when the in-slice candidate matches the GT.
func TestAmbiguousUnitStraddlesSliceBoundary(t *testing.T) {
	rep := mustScore(t, scoreInput{
		InSlice: set(1, 2), // 9 is out of slice
		TS:      []tsRow{tsAmb(1, 2, "m", 1), tsAmb(1, 9, "m", 1)},
		GT:      []gtRow{gt(1, 2, graph.KindInterfaceMethod)},
	})
	if rep.Counts.ScoredInSlice != 1 || rep.Counts.ResolvedOutOfSlice != 1 {
		t.Errorf("buckets = %+v, want one in-slice and one out-of-slice edge", rep.Counts)
	}
	if rep.Counts.PrecisionUniverse != 1 || rep.Counts.TP != 1 {
		t.Errorf("straddling unit not scored/TP: universe %d, tp %d", rep.Counts.PrecisionUniverse, rep.Counts.TP)
	}
	if val(t, rep.Precision) != 1 || val(t, rep.Recall) != 1 {
		t.Errorf("precision/recall = %v/%v, want 1/1", rep.Precision, rep.Recall)
	}
}

// TestByKindSumsToOverallUniverses: the per-kind counters partition the overall
// universes (a pair is attributed to exactly one kind).
func TestByKindSumsToOverallUniverses(t *testing.T) {
	rep := mustScore(t, goldenFixture())
	var scored, tp, gtPairs, found int
	for _, st := range rep.ByKind {
		scored += st.ScoredUnits
		tp += st.TP
		gtPairs += st.GTPairs
		found += st.Found
	}
	if scored != rep.Counts.PrecisionUniverse {
		t.Errorf("scored units %d != precision universe %d", scored, rep.Counts.PrecisionUniverse)
	}
	if tp != rep.Counts.TP {
		t.Errorf("tp %d != overall tp %d", tp, rep.Counts.TP)
	}
	if gtPairs != rep.Counts.RecallUniverse {
		t.Errorf("gt pairs %d != recall universe %d", gtPairs, rep.Counts.RecallUniverse)
	}
	if found != rep.Counts.Found {
		t.Errorf("found %d != overall found %d", found, rep.Counts.Found)
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
