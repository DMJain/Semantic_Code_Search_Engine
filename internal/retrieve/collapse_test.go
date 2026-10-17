package retrieve

import "testing"

// TestCollapseToFiles pins the grouping, scoring, evidence order, tie break
// and limit of the collapse helper.
func TestCollapseToFiles(t *testing.T) {
	hits := []Hit{
		{File: "a.go", ChunkID: 2, Score: 0.5},
		{File: "a.go", ChunkID: 1, Score: 0.9},
		{File: "b.go", ChunkID: 3, Score: 0.7},
		{File: "c.go", ChunkID: 4, Score: 0.7},
	}
	got := CollapseToFiles(hits, CollapseOpts{Limit: 10})
	if len(got) != 3 {
		t.Fatalf("results = %d, want 3", len(got))
	}
	// a.go wins with its best hit and keeps both hits, best first.
	if got[0].File != "a.go" || got[0].Score != 0.9 {
		t.Errorf("result[0] = {%s %.2f}, want {a.go 0.9}", got[0].File, got[0].Score)
	}
	if len(got[0].Evidence) != 2 || got[0].Evidence[0].ChunkID != 1 || got[0].Evidence[1].ChunkID != 2 {
		t.Errorf("a.go evidence = %+v, want chunk 1 then 2", got[0].Evidence)
	}
	// b.go and c.go tie on score; the path breaks the tie.
	if got[1].File != "b.go" || got[2].File != "c.go" {
		t.Errorf("tie order = %s, %s; want b.go, c.go", got[1].File, got[2].File)
	}
}

// TestCollapseToFilesEvidenceTieOrder pins evidence ordering when two hits of
// one file share a score: the lower chunk id comes first.
func TestCollapseToFilesEvidenceTieOrder(t *testing.T) {
	got := CollapseToFiles([]Hit{
		{File: "a.go", ChunkID: 9, Score: 0.5},
		{File: "a.go", ChunkID: 4, Score: 0.5},
	}, CollapseOpts{})
	if len(got) != 1 || len(got[0].Evidence) != 2 {
		t.Fatalf("got %+v, want one file with two hits", got)
	}
	if got[0].Evidence[0].ChunkID != 4 || got[0].Evidence[1].ChunkID != 9 {
		t.Errorf("evidence = %+v, want chunk 4 then 9", got[0].Evidence)
	}
}

// TestCollapseToFilesEmptyAndLimit pins the empty input and the cut.
func TestCollapseToFilesEmptyAndLimit(t *testing.T) {
	if got := CollapseToFiles(nil, CollapseOpts{Limit: 10}); len(got) != 0 {
		t.Errorf("empty input = %+v, want no results", got)
	}
	got := CollapseToFiles([]Hit{
		{File: "a.go", Score: 0.9},
		{File: "b.go", Score: 0.8},
		{File: "c.go", Score: 0.7},
	}, CollapseOpts{Limit: 2})
	if len(got) != 2 || got[0].File != "a.go" || got[1].File != "b.go" {
		t.Errorf("limit cut = %+v, want a.go then b.go", got)
	}
}

// TestCollapseToFilesFiltersBeforeCut proves the slice filter runs before the
// grouping and the cut: the ten best hits are out of slice, so only the two
// in-slice hits survive rather than an empty list.
func TestCollapseToFilesFiltersBeforeCut(t *testing.T) {
	var hits []Hit
	for i := 0; i < 10; i++ {
		hits = append(hits, Hit{File: "outside.go", ChunkID: int64(i), Score: 1.0 - float64(i)*0.01})
	}
	hits = append(hits,
		Hit{File: "in1.go", ChunkID: 100, Score: 0.2},
		Hit{File: "in2.go", ChunkID: 101, Score: 0.1},
	)
	inSlice := map[string]bool{"in1.go": true, "in2.go": true}
	got := CollapseToFiles(hits, CollapseOpts{
		SliceOnly: true,
		InSlice:   func(p string) bool { return inSlice[p] },
		Limit:     10,
	})
	if len(got) != 2 {
		t.Fatalf("results = %d, want the 2 in-slice files", len(got))
	}
	if got[0].File != "in1.go" || got[1].File != "in2.go" {
		t.Errorf("results = %s, %s; want in1.go, in2.go", got[0].File, got[1].File)
	}
}

// TestCollapseToFilesNilFilterDropsAll pins the fail-closed slice filter: with
// SliceOnly set and InSlice nil, every hit is treated as out of slice and
// dropped rather than silently ignoring the filter.
func TestCollapseToFilesNilFilterDropsAll(t *testing.T) {
	got := CollapseToFiles([]Hit{
		{File: "a.go", ChunkID: 1, Score: 0.9},
		{File: "b.go", ChunkID: 2, Score: 0.8},
	}, CollapseOpts{SliceOnly: true, Limit: 10})
	if len(got) != 0 {
		t.Errorf("results = %+v, want none with a nil slice filter", got)
	}
}
