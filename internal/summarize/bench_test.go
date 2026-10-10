package summarize

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
)

// TestSampleFilesDeterministic pins that SampleFiles is reproducible: the same
// candidates and seed return the same paths whatever the input order.
func TestSampleFilesDeterministic(t *testing.T) {
	cands := fakeFileCandidates(t, 30, 20, 20, 20, 20)
	first := SampleFiles(cands, 20, 7)

	reversed := make([]FileCandidate, len(cands))
	for i, c := range cands {
		reversed[len(cands)-1-i] = c
	}
	second := SampleFiles(reversed, 20, 7)
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("SampleFiles not deterministic:\n%v\n%v", first, second)
	}
	if third := SampleFiles(cands, 20, 7); fmt.Sprint(third) != fmt.Sprint(first) {
		t.Fatalf("SampleFiles not repeatable:\n%v\n%v", first, third)
	}
}

// TestSampleFilesStrata checks that a 20-file draw covers at least three
// distinct eligible-function-count strata and never repeats a path.
func TestSampleFilesStrata(t *testing.T) {
	cands := fakeFileCandidates(t, 30, 20, 20, 20, 20)
	got := SampleFiles(cands, 20, 7)
	if len(got) != 20 {
		t.Fatalf("sample size = %d, want 20", len(got))
	}
	seen := map[string]bool{}
	for _, f := range got {
		if seen[f.Path] {
			t.Fatalf("path %s returned twice", f.Path)
		}
		seen[f.Path] = true
	}
	if strata := distinctStrata(got); len(strata) < 3 {
		t.Fatalf("file sample covers %d strata (%v), want >= 3", len(strata), strata)
	}
}

// TestFileStratumBoundaries pins the bucket edges.
func TestFileStratumBoundaries(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{1, "1-2"}, {2, "1-2"}, {3, "3-5"}, {5, "3-5"},
		{6, "6-11"}, {11, "6-11"}, {12, "12-23"}, {23, "12-23"}, {24, "24+"}, {193, "24+"},
	}
	for _, tc := range cases {
		if got := FileStratum(tc.n); got != tc.want {
			t.Errorf("FileStratum(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// TestBenchDoesNotInsertSummaries runs the benchmark end to end against a
// throwaway database and a fake Ollama server, then asserts the summaries table
// is still empty.
func TestBenchDoesNotInsertSummaries(t *testing.T) {
	pool := testdb.New(t)
	dir := t.TempDir()
	source, spans := funcFile("B00", "B01", "B02", "B03")
	seedSource(t, pool, dir, "pkg/scheduler/bench.go", source, spans)

	fake := newFakeOllama(t, nil)
	r := newRunner(pool, fake.URL, dir, 4096, io.Discard)

	res, err := r.Bench(context.Background(), BenchOptions{FunctionN: 4, FileN: 1, PkgN: 1, Seed: 7})
	if err != nil {
		t.Fatalf("bench: %v", err)
	}
	if res.SliceFnsEligible != 4 {
		t.Fatalf("slice_fns_eligible = %d, want 4", res.SliceFnsEligible)
	}
	if res.FnPerHourC1 <= 0 || res.FnPerHourC2 <= 0 || res.FilePerHour <= 0 || res.PkgPerHour <= 0 {
		t.Fatalf("throughputs not positive: c1=%v c2=%v file=%v pkg=%v",
			res.FnPerHourC1, res.FnPerHourC2, res.FilePerHour, res.PkgPerHour)
	}
	if got := summariesTotal(t, pool); got != 0 {
		t.Fatalf("summaries rows after bench = %d, want 0", got)
	}
}

// fakeFileCandidates builds deterministically named candidates in the five
// eligible-function strata: n1 files with 1 eligible fn, n2 with 4, n3 with 8,
// n4 with 15 and n5 with 30.
func fakeFileCandidates(t *testing.T, n1, n2, n3, n4, n5 int) []FileCandidate {
	t.Helper()
	counts := []int{1, 4, 8, 15, 30}
	sizes := []int{n1, n2, n3, n4, n5}
	var out []FileCandidate
	id := 0
	for i, size := range sizes {
		for j := 0; j < size; j++ {
			id++
			out = append(out, FileCandidate{
				Path:     fmt.Sprintf("pkg/root%02d/file%04d.go", i, id),
				Package:  fmt.Sprintf("pkg/root%02d", i),
				Eligible: counts[i],
			})
		}
	}
	return out
}

// summariesTotal returns the total number of rows in the summaries table.
func summariesTotal(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM summaries`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
