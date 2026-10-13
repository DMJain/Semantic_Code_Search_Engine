package graph

import (
	"sort"
)

// CandidateMode selects how the Rule 4 candidate set is bounded. The cap
// histogram only uses UncappedCandidates; the cap itself is applied later.
type CandidateMode int

const (
	// UncappedCandidates returns every candidate; n is ignored.
	UncappedCandidates CandidateMode = iota
	// CappedCandidates returns the full sorted candidate set and reports capped
	// when it exceeds n (once the cap lands).
	CappedCandidates
)

// Rule4 configures interface-receiver (Rule 4) handling in Analyze. The zero
// value is the current production behaviour: no Rule 4 rows, the call site is
// counted deferred_interface. Compute enables the candidate rows (the histogram
// dry run and the test-only uncapped fixture path).
type Rule4 struct {
	// Compute enables Rule 4 resolution.
	Compute bool
	// N caps the candidate set when > 0 (applied once the cap lands); <= 0 is
	// uncapped.
	N int
	// Observe, when set, is called once per interface call site with the number
	// of candidates actually stored and whether an embedded interface was
	// unresolvable. Used by the histogram dry run.
	Observe func(count int, partial bool)
}

// InterfaceCandidates returns the concrete indexed types that declare every
// known method name of the interface (pkg, name): value and pointer receivers
// both count, interfaces are excluded and signatures are not compared (D-075).
// partial reports an unresolvable (cross-package or unknown) embedded interface,
// whose method names are absent so the candidate set is a superset. An interface
// with no known method name yields no candidates. n with CappedCandidates
// reports capped when more than n candidates exist; the set is always sorted
// first, so it is deterministic.
func (ix *Index) InterfaceCandidates(names *NameIndex, pkg, name string, n int, mode CandidateMode) (cands []TypeKey, partial, capped bool) {
	required, partial, ok := names.InterfaceMethodNames(pkg, name)
	if !ok || len(required) == 0 {
		return nil, partial, false
	}
	// Start from the required name with the fewest declaring types, then keep
	// only the types that declare every other required name.
	var base []TypeKey
	for m := range required {
		list := ix.typesWithMethod[m]
		if base == nil || len(list) < len(base) {
			base = list
		}
	}
	for _, t := range base {
		if ix.interfaces[pkgName(t.Pkg, t.Type)] {
			continue
		}
		if !hasAllMethods(ix.methodsOnType[pkgName(t.Pkg, t.Type)], required) {
			continue
		}
		cands = append(cands, t)
	}
	// Sort before capping, so a capped result is deterministic too.
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].Pkg != cands[j].Pkg {
			return cands[i].Pkg < cands[j].Pkg
		}
		return cands[i].Type < cands[j].Type
	})
	if mode == CappedCandidates && n > 0 && len(cands) > n {
		return cands, partial, true
	}
	return cands, partial, false
}

// hasAllMethods reports whether the type's method-name set contains every
// required name.
func hasAllMethods(have, required map[string]bool) bool {
	for m := range required {
		if !have[m] {
			return false
		}
	}
	return true
}

// analyzeInterface resolves an interface-typed receiver (Rule 4). In production
// (r4.Compute false) the call site stays deferred_interface and stores no row.
// With r4.Compute it stores one ambiguous interface_method row per candidate;
// zero candidates store no row and stay deferred_interface; more than the cap
// stores one candidates_capped row.
func analyzeInterface(edges []Edge, st Stats, callerID int64, c Call, pkg, name string, ix *Index, names *NameIndex, r4 Rule4) ([]Edge, Stats) {
	if !r4.Compute {
		st.DeferredInterface++
		return edges, st
	}
	mode := UncappedCandidates
	if r4.N > 0 {
		mode = CappedCandidates
	}
	cands, partial, capped := ix.InterfaceCandidates(names, pkg, name, r4.N, mode)
	if partial {
		st.InterfacesPartial++
	}
	if capped {
		if r4.Observe != nil {
			r4.Observe(r4.N+1, partial)
		}
		st.Unresolved++
		st.Reasons[ReasonCandidatesCap]++
		st.ByKind[KindInterfaceMethod]++
		return append(edges, Edge{
			CallerID: callerID, CalleeID: 0, CalleeRaw: c.Raw,
			Resolution: ResolutionUnresolved, CallKind: KindInterfaceMethod,
			CallLine: c.Line, UnresolvedReason: ReasonCandidatesCap,
		}), st
	}
	stored := 0
	for _, cand := range cands {
		id, ok := ix.ConcreteMethod(cand.Pkg, cand.Type, c.Field)
		if !ok {
			continue
		}
		edges = append(edges, Edge{
			CallerID: callerID, CalleeID: id, CalleeRaw: c.Raw,
			Resolution: ResolutionAmbiguous, CallKind: KindInterfaceMethod, CallLine: c.Line,
		})
		stored++
	}
	if r4.Observe != nil {
		r4.Observe(stored, partial)
	}
	if stored == 0 {
		// No candidate (or none declaring the called name): no Rule 4 rows. The
		// residual is counted deferred_interface, which the cap step removes.
		st.DeferredInterface++
		return edges, st
	}
	st.Ambiguous += stored
	st.ByKind[KindInterfaceMethod] += stored
	st.CallSites += stored - 1
	return edges, st
}
