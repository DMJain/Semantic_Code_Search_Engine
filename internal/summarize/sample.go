package summarize

import (
	"slices"
	"sort"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/seeded"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/walk"
)

// This file is the deterministic stratified sampling of functions and files;
// it changes only when the sampling rules change.

// Candidate is one eligible function offered to [Sample]: its symbol id, its
// qualified name (the deterministic ordering key) and its slice root.
type Candidate struct {
	ID            int64
	QualifiedName string
	Root          string
}

// Sample selects at most n candidate ids deterministically. Candidates are
// grouped by Root in walk.SliceRoots order; the n slots are allocated across
// the roots in proportion to each root's eligible count by the largest-
// remainder (Hare quota) method; within a root the candidates, sorted by
// QualifiedName, are shuffled with a math/rand generator seeded by seed and the
// first allocation are taken. The same (candidates, n, seed) always returns the
// same ids in the same order. The result is smaller than n only when fewer
// candidates exist.
func Sample(cands []Candidate, n int, seed int64) []int64 {
	if n <= 0 || len(cands) == 0 {
		return nil
	}
	ordered := slices.Clone(cands)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].QualifiedName < ordered[j].QualifiedName
	})
	if n >= len(ordered) {
		ids := make([]int64, len(ordered))
		for i, c := range ordered {
			ids[i] = c.ID
		}
		return ids
	}

	roots := rootOrder(ordered)
	byRoot := make(map[string][]Candidate, len(roots))
	for _, c := range ordered {
		byRoot[c.Root] = append(byRoot[c.Root], c)
	}
	quotas := allocate(n, byRoot, roots)

	groups := make([][]Candidate, len(roots))
	qs := make([]int, len(roots))
	for i, root := range roots {
		groups[i], qs[i] = byRoot[root], quotas[root]
	}
	var ids []int64
	for _, c := range seeded.Stratified(groups, qs, seed) {
		ids = append(ids, c.ID)
	}
	return ids
}

// allocate distributes n slots over the strata in proportion to their sizes by
// the largest-remainder method: each root gets floor(n*size/total) slots, and
// the leftover slots go to the roots with the largest remainders, ties broken
// by roots order.
func allocate(n int, byRoot map[string][]Candidate, roots []string) map[string]int {
	sizes := make(map[string]int, len(roots))
	for _, root := range roots {
		sizes[root] = len(byRoot[root])
	}
	return allocateCounts(n, sizes, roots)
}

// allocateCounts distributes n slots over the keys in order in proportion to
// their sizes by the largest-remainder (Hare quota) method: each key gets
// floor(n*size/total) slots, and the leftover slots go to the keys with the
// largest remainders, ties broken by order. Every size is assumed present in
// sizes; a zero total returns empty quotas.
func allocateCounts(n int, sizes map[string]int, order []string) map[string]int {
	total := 0
	for _, key := range order {
		total += sizes[key]
	}
	quotas := make(map[string]int, len(order))
	if total == 0 {
		return quotas
	}
	type remainder struct {
		key   string
		value int
		rank  int
	}
	rems := make([]remainder, 0, len(order))
	assigned := 0
	for i, key := range order {
		size := sizes[key]
		q := n * size / total
		quotas[key] = q
		assigned += q
		rems = append(rems, remainder{key: key, value: n*size - q*total, rank: i})
	}
	sort.SliceStable(rems, func(i, j int) bool {
		if rems[i].value != rems[j].value {
			return rems[i].value > rems[j].value
		}
		return rems[i].rank < rems[j].rank
	})
	for i := 0; i < n-assigned && i < len(rems); i++ {
		quotas[rems[i].key]++
	}
	return quotas
}

// rootOrder returns the roots present in ordered, walk.SliceRoots first in
// their configured order, then any other roots (there should be none) sorted by
// name so the result is independent of map iteration.
func rootOrder(ordered []Candidate) []string {
	seen := make(map[string]bool)
	for _, c := range ordered {
		seen[c.Root] = true
	}
	var roots []string
	sliceRoots := walk.SliceRoots()
	known := make(map[string]bool, len(sliceRoots))
	for _, p := range sliceRoots {
		known[p] = true
		if seen[p] {
			roots = append(roots, p)
		}
	}
	var extra []string
	for root := range seen {
		if !known[root] {
			extra = append(extra, root)
		}
	}
	sort.Strings(extra)
	return append(roots, extra...)
}

// sampleCandidates maps eligible rows to [Candidate], assigning each its slice
// root.
func sampleCandidates(rows []symbolRow) []Candidate {
	out := make([]Candidate, len(rows))
	for i, sr := range rows {
		out[i] = Candidate{ID: sr.id, QualifiedName: sr.qualifiedName, Root: walk.SliceRootOf(sr.path)}
	}
	return out
}

// filterByID keeps the rows whose id is in ids.
func filterByID(rows []symbolRow, ids []int64) []symbolRow {
	set := make(map[int64]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	out := make([]symbolRow, 0, len(ids))
	for _, sr := range rows {
		if set[sr.id] {
			out = append(out, sr)
		}
	}
	return out
}

// FileStratum buckets a file by its eligible-function count. The boundaries
// roughly double so each stratum is a distinct size class.
func FileStratum(eligible int) string {
	switch {
	case eligible <= 2:
		return "1-2"
	case eligible <= 5:
		return "3-5"
	case eligible <= 11:
		return "6-11"
	case eligible <= 23:
		return "12-23"
	default:
		return "24+"
	}
}

// fileStrataOrder is the smallest-to-largest stratum order used by [SampleFiles].
var fileStrataOrder = []string{"1-2", "3-5", "6-11", "12-23", "24+"}

// SampleFiles selects at most n files deterministically, stratified by
// [FileStratum]. Files are sorted by Path; the n slots are allocated across the
// strata in proportion to each stratum's size by the largest-remainder method,
// and within a stratum the files are shuffled with a math/rand generator seeded
// by seed before the first allocation are taken. The same (files, n, seed)
// always returns the same paths in the same order.
func SampleFiles(files []FileCandidate, n int, seed int64) []FileCandidate {
	if n <= 0 || len(files) == 0 {
		return nil
	}
	ordered := slices.Clone(files)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	if n >= len(ordered) {
		return ordered
	}

	byStratum := make(map[string][]FileCandidate)
	for _, f := range ordered {
		s := FileStratum(f.Eligible)
		byStratum[s] = append(byStratum[s], f)
	}
	sizes := make(map[string]int, len(byStratum))
	var present []string
	for _, s := range fileStrataOrder {
		if len(byStratum[s]) > 0 {
			sizes[s] = len(byStratum[s])
			present = append(present, s)
		}
	}
	quotas := allocateCounts(n, sizes, present)

	groups := make([][]FileCandidate, len(present))
	qs := make([]int, len(present))
	for i, s := range present {
		groups[i], qs[i] = byStratum[s], quotas[s]
	}
	return seeded.Stratified(groups, qs, seed)
}
