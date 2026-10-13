package graph

import (
	"sort"
)

// MaxInterfaceCandidates caps the number of ambiguous Rule 4 rows one interface
// call site may store; beyond it the call is stored once as candidates_capped
// (D-075 records N = 50 from eval/results/callgraph/cap-histogram.json).
const MaxInterfaceCandidates = 50

// CandidateMode selects how the Rule 4 candidate set is bounded. The cap
// histogram only uses UncappedCandidates; production uses CappedCandidates with
// N = MaxInterfaceCandidates.
type CandidateMode int

const (
	// UncappedCandidates returns every candidate; n is ignored.
	UncappedCandidates CandidateMode = iota
	// CappedCandidates returns the full sorted candidate set and reports capped
	// when it exceeds n.
	CappedCandidates
)

// Rule4 configures interface-receiver (Rule 4) handling in Analyze. Rule 4 is
// always computed; N caps the candidate set and Observe, when set, is the
// histogram dry-run hook.
type Rule4 struct {
	// N caps the candidate set when > 0; <= 0 is uncapped (the histogram dry
	// run). Production uses MaxInterfaceCandidates.
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
	cands = ix.candidatesFor(required)
	if mode == CappedCandidates && capExceeded(cands, n) {
		return cands, partial, true
	}
	return cands, partial, false
}

// capExceeded is the single Rule 4 cap decision shared by production
// (analyzeInterface) and Index.InterfaceCandidates, so the two cannot drift:
// a non-positive n is uncapped, otherwise the set is over the cap when it holds
// more than n candidates.
func capExceeded(cands []TypeKey, n int) bool {
	return n > 0 && len(cands) > n
}

// candidatesFor returns the sorted concrete indexed types whose method-name set
// contains every required name: value and pointer receivers count, interfaces
// are excluded (D-075). The set is sorted so a capped result is deterministic.
func (ix *Index) candidatesFor(required map[string]bool) []TypeKey {
	// Start from the required name with the fewest declaring types, then keep
	// only the types that declare every other required name.
	var base []TypeKey
	for m := range required {
		list := ix.typesWithMethod[m]
		if base == nil || len(list) < len(base) {
			base = list
		}
	}
	var cands []TypeKey
	for _, t := range base {
		if ix.interfaces[pkgName(t.Pkg, t.Type)] {
			continue
		}
		if !hasAllMethods(ix.methodsOnType[pkgName(t.Pkg, t.Type)], required) {
			continue
		}
		cands = append(cands, t)
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].Pkg != cands[j].Pkg {
			return cands[i].Pkg < cands[j].Pkg
		}
		return cands[i].Type < cands[j].Type
	})
	return cands
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

// analyzeInterface resolves an interface-typed receiver (Rule 4). An interface
// with no known method name yields no Rule 4 rows and falls through to Rule 5
// (spec story 50), so the call site stays accounted for. Otherwise: up to N
// candidates store one ambiguous interface_method row per candidate (D-023);
// more than N store one candidates_capped unresolved row with no callee and the
// raw text kept; a known interface with candidates but none declaring the called
// name stores one no_match unresolved row (the interface is indexed but no
// implementing type matched). callerPkg is the caller file's package, used by
// the Rule 5 fallback.
func analyzeInterface(edges []Edge, st Stats, callerID int64, c Call, callerPkg, ifacePkg, name string, ix *Index, names *NameIndex, r4 Rule4) ([]Edge, Stats) {
	required, partial, ok := names.InterfaceMethodNames(ifacePkg, name)
	if !ok || len(required) == 0 {
		// Zero known methods (or an interface the name index does not know):
		// Rule 4 must never match every type on an empty set, so the call is
		// accounted for by Rule 5 instead.
		return analyzeUnknownReceiver(edges, st, callerID, c, callerPkg, ix)
	}
	cands := ix.candidatesFor(required)
	if partial {
		st.InterfacesPartial++
	}
	if capExceeded(cands, r4.N) {
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
		// The interface is indexed and has known methods, but no indexed type
		// declares them, so the call is honestly unresolved.
		st.Unresolved++
		st.Reasons[ReasonNoMatch]++
		st.ByKind[KindInterfaceMethod]++
		return append(edges, Edge{
			CallerID: callerID, CalleeID: 0, CalleeRaw: c.Raw,
			Resolution: ResolutionUnresolved, CallKind: KindInterfaceMethod,
			CallLine: c.Line, UnresolvedReason: ReasonNoMatch,
		}), st
	}
	st.Ambiguous += stored
	st.ByKind[KindInterfaceMethod] += stored
	st.CallSites += stored - 1
	return edges, st
}
