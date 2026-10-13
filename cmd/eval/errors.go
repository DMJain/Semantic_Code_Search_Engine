package main

import (
	"sort"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/seeded"
)

// The error-analysis sample (spec story 89): 30 false positives and 30 false
// negatives, seed 11, stratified by call kind. The rules mirror the report: a
// false positive is a scored tree-sitter unit (one with an in-slice candidate)
// that no ground-truth pair matches; a false negative is an in-slice
// ground-truth pair no tree-sitter unit covers.
const (
	sampleSeed     = 11
	samplePerClass = 30
)

// fpUnit is one false-positive tree-sitter call unit. It is produced by score()
// so the sampler and the report cannot disagree.
type fpUnit struct {
	CallerID int64
	Kind     string
	Raw      string
	Line     int
	CandIDs  []int64
}

// fnPair is one false-negative ground-truth pair.
type fnPair struct {
	CallerID int64
	CalleeID int64
	Kind     string
	Line     int
}

// enumerateErrors runs the scoring core and returns the error lists it built,
// so the sampler reuses the report's exact FP/FN rules.
func enumerateErrors(in scoreInput) ([]fpUnit, []fnPair, error) {
	rep, err := score(in)
	if err != nil {
		return nil, nil, err
	}
	return rep.fpUnits, rep.fnPairs, nil
}

// allocate splits total across strata of the given sizes proportionally, using
// the largest-remainder method, never exceeding a stratum's size and dropping
// any remainder that does not fit. Ties break by stratum order. It is the
// quota rule for the stratified draw.
func allocate(total int, sizes []int) []int {
	sum := 0
	for _, s := range sizes {
		sum += s
	}
	q := make([]int, len(sizes))
	if sum == 0 || total <= 0 {
		return q
	}
	assigned := 0
	for i, s := range sizes {
		q[i] = min(total*s/sum, s)
		assigned += q[i]
	}
	for assigned < total {
		best := -1
		bestFrac := 0.0
		for i, s := range sizes {
			if q[i] >= s {
				continue
			}
			// The exact share minus what is already assigned: the largest
			// remainder gets the next seat.
			frac := float64(total)*float64(s)/float64(sum) - float64(q[i])
			if best == -1 || frac > bestFrac {
				best, bestFrac = i, frac
			}
		}
		if best == -1 {
			break // no stratum has room
		}
		q[best]++
		assigned++
	}
	return q
}

// sampleErrors draws up to samplePerClass false positives and false negatives,
// stratified by kind with allocate() quotas and the seeded package at seed 11.
// Each stratum is sorted first, so the draw is deterministic whatever order the
// database returned rows in.
func sampleErrors(fps []fpUnit, fns []fnPair) (fpSel []fpUnit, fnSel []fnPair, fpQuota, fnQuota map[string]int) {
	fpGroups, fpKinds := groupFP(fps)
	fnGroups, fnKinds := groupFN(fns)
	fpQ := allocate(samplePerClass, groupSizesFP(fpGroups))
	fnQ := allocate(samplePerClass, groupSizesFN(fnGroups))
	return seeded.Stratified(fpGroups, fpQ, sampleSeed),
		seeded.Stratified(fnGroups, fnQ, sampleSeed),
		quotaMap(fpKinds, fpQ), quotaMap(fnKinds, fnQ)
}

// groupFP buckets false positives by kind in the stable report order, sorting
// each bucket by a stable key so the seeded shuffle is reproducible.
func groupFP(fps []fpUnit) ([][]fpUnit, []string) {
	byKind := map[string][]fpUnit{}
	for _, u := range fps {
		byKind[u.Kind] = append(byKind[u.Kind], u)
	}
	kinds := orderedKinds(keysFP(byKind))
	groups := make([][]fpUnit, 0, len(kinds))
	for _, k := range kinds {
		g := byKind[k]
		sort.Slice(g, func(i, j int) bool { return lessFP(g[i], g[j]) })
		groups = append(groups, g)
	}
	return groups, kinds
}

func groupFN(fns []fnPair) ([][]fnPair, []string) {
	byKind := map[string][]fnPair{}
	for _, p := range fns {
		byKind[p.Kind] = append(byKind[p.Kind], p)
	}
	kinds := orderedKinds(keysFN(byKind))
	groups := make([][]fnPair, 0, len(kinds))
	for _, k := range kinds {
		g := byKind[k]
		sort.Slice(g, func(i, j int) bool { return lessFN(g[i], g[j]) })
		groups = append(groups, g)
	}
	return groups, kinds
}

func lessFP(a, b fpUnit) bool {
	if a.CallerID != b.CallerID {
		return a.CallerID < b.CallerID
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	if a.Raw != b.Raw {
		return a.Raw < b.Raw
	}
	return a.Kind < b.Kind
}

func lessFN(a, b fnPair) bool {
	if a.CallerID != b.CallerID {
		return a.CallerID < b.CallerID
	}
	if a.CalleeID != b.CalleeID {
		return a.CalleeID < b.CalleeID
	}
	return a.Kind < b.Kind
}

// orderedKinds returns the kinds in the report's stable order, then any extras
// sorted, so stratification is deterministic.
func orderedKinds(present []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range callKinds {
		if contains(present, k) {
			out = append(out, k)
			seen[k] = true
		}
	}
	for _, k := range present {
		if !seen[k] {
			out = append(out, k)
			seen[k] = true
		}
	}
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func keysFP(m map[string][]fpUnit) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keysFN(m map[string][]fnPair) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func groupSizesFP(groups [][]fpUnit) []int {
	out := make([]int, len(groups))
	for i, g := range groups {
		out[i] = len(g)
	}
	return out
}

func groupSizesFN(groups [][]fnPair) []int {
	out := make([]int, len(groups))
	for i, g := range groups {
		out[i] = len(g)
	}
	return out
}

func quotaMap(kinds []string, quotas []int) map[string]int {
	m := map[string]int{}
	for i, k := range kinds {
		m[k] = quotas[i]
	}
	return m
}
