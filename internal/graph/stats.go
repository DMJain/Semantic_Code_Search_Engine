package graph

import (
	"fmt"
	"math"
	"sort"
)

// CandidateCountThresholds are the fixed cap candidates the N rule chooses
// from (D-075).
var CandidateCountThresholds = []int{5, 10, 20, 50, 100}

// maxNonEscalatingCap is the largest N the rule may adopt; a chosen N above it
// stops the work and escalates to the owner (D-075). It is the Rule 4 production
// cap by definition, so it references MaxInterfaceCandidates.
const maxNonEscalatingCap = MaxInterfaceCandidates

// CapHistogram collects the candidate count of every interface call site during
// an uncapped Rule 4 dry run, to choose the cap N from evidence (D-075).
type CapHistogram struct {
	counts  []int
	Partial int
}

// NewCapHistogram returns an empty histogram.
func NewCapHistogram() *CapHistogram { return &CapHistogram{} }

// Add records one interface call site's candidate count.
func (h *CapHistogram) Add(count int) { h.counts = append(h.counts, count) }

// Observe is the Rule4.Observe hook: it records the count and a partial
// interface (a phase-appropriate adapter).
func (h *CapHistogram) Observe(count int, partial bool) {
	h.Add(count)
	if partial {
		h.Partial++
	}
}

// Sites is the number of interface call sites recorded.
func (h *CapHistogram) Sites() int { return len(h.counts) }

// CandidateTotal is the sum of the candidate counts, the number of ambiguous
// Rule 4 rows an uncapped run would store.
func (h *CapHistogram) CandidateTotal() int {
	n := 0
	for _, c := range h.counts {
		n += c
	}
	return n
}

// AtMost is the number of call sites with at most n candidates.
func (h *CapHistogram) AtMost(n int) int {
	count := 0
	for _, c := range h.counts {
		if c <= n {
			count++
		}
	}
	return count
}

// Percentile returns the nearest-rank percentile (0-100) of the candidate
// counts: the smallest count at or above that fraction of sites.
func (h *CapHistogram) Percentile(p float64) int {
	if len(h.counts) == 0 {
		return 0
	}
	sorted := append([]int(nil), h.counts...)
	sort.Ints(sorted)
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// Max is the largest candidate count.
func (h *CapHistogram) Max() int {
	largest := 0
	for _, c := range h.counts {
		largest = max(largest, c)
	}
	return largest
}

// ChooseN applies the fixed rule (D-075): the smallest of {5, 10, 20, 50, 100}
// such that at least 95% of interface call sites have at most N candidates, or 0
// when none qualifies. Result reports the escalation when the choice is 0 or
// above maxNonEscalatingCap.
func (h *CapHistogram) ChooseN() int {
	if len(h.counts) == 0 {
		return 0
	}
	need := int(math.Ceil(0.95 * float64(len(h.counts))))
	for _, n := range CandidateCountThresholds {
		if h.AtMost(n) >= need {
			return n
		}
	}
	return 0
}

// CapHistogramResult is the committed cap-histogram evidence file (D-075).
type CapHistogramResult struct {
	ResolverVersion   int `json:"resolver_version"`
	InterfaceSites    int `json:"interface_sites"`
	CandidateTotal    int `json:"candidate_total"`
	InterfacesPartial int `json:"interfaces_partial"`
	P50               int `json:"p50"`
	P90               int `json:"p90"`
	P99               int `json:"p99"`
	Max               int `json:"max"`
	AtMost5           int `json:"at_most_5"`
	AtMost10          int `json:"at_most_10"`
	AtMost20          int `json:"at_most_20"`
	AtMost50          int `json:"at_most_50"`
	AtMost100         int `json:"at_most_100"`
	ChosenN           int `json:"chosen_n"`
	// Escalate is set when no threshold qualifies (ChosenN 0) or the chosen N
	// is above maxNonEscalatingCap: the work stops and the owner is asked.
	Escalate bool   `json:"escalate"`
	Rule     string `json:"rule"`
}

// Result renders the histogram as the committed evidence record.
func (h *CapHistogram) Result(resolverVersion int) CapHistogramResult {
	n := h.ChooseN()
	return CapHistogramResult{
		ResolverVersion:   resolverVersion,
		InterfaceSites:    h.Sites(),
		CandidateTotal:    h.CandidateTotal(),
		InterfacesPartial: h.Partial,
		P50:               h.Percentile(50),
		P90:               h.Percentile(90),
		P99:               h.Percentile(99),
		Max:               h.Max(),
		AtMost5:           h.AtMost(5),
		AtMost10:          h.AtMost(10),
		AtMost20:          h.AtMost(20),
		AtMost50:          h.AtMost(50),
		AtMost100:         h.AtMost(100),
		ChosenN:           n,
		Escalate:          n == 0 || n > maxNonEscalatingCap,
		Rule:              "smallest of {5,10,20,50,100} such that at least 95% of interface call sites have at most N candidates",
	}
}

// Stats reconciles call sites for one graph run. Every unique call site
// (enclosing caller, raw callee text, call line) is counted exactly once, so
// CallSites equals the sum of stored resolutions plus the skip and deferred
// counters (the S2 invariant). An ambiguous call site stores one row per
// candidate, and each such row counts as one call unit, so CallSites equals the
// stored rows plus the skip and deferred counters. The per-(resolution, reason)
// counts sum to the stored rows.
type Stats struct {
	// CallSites is the number of call units seen: one per unique call site, plus
	// one extra per additional ambiguous candidate row.
	CallSites int
	// Stored rows by resolution.
	Exact      int
	NameOnly   int
	Ambiguous  int
	Unresolved int
	// Reasons counts stored unresolved rows by reason.
	Reasons map[string]int
	// ByKind counts stored rows by call kind.
	ByKind map[string]int
	// Skip counters: call sites deliberately not stored.
	SkippedBuiltin    int
	SkippedConversion int
	// Deferred counters: kinds not yet handled, never counted as no_match.
	DeferredLocal int
	// InterfacesPartial counts Rule 4 call sites whose interface has a
	// cross-package or unresolvable embedded interface, so the candidate set is
	// a superset (D-075).
	InterfacesPartial int
}

// NewStats returns an empty Stats with its maps ready.
func NewStats() Stats {
	return Stats{Reasons: map[string]int{}, ByKind: map[string]int{}}
}

// Stored is the number of stored edges (all resolutions with a row).
func (s Stats) Stored() int { return s.Exact + s.NameOnly + s.Ambiguous + s.Unresolved }

// Skipped is the number of call sites counted by skip counters.
func (s Stats) Skipped() int { return s.SkippedBuiltin + s.SkippedConversion }

// Deferred is the number of call sites counted by deferred counters.
func (s Stats) Deferred() int {
	return s.DeferredLocal
}

// Add merges another Stats into s.
func (s *Stats) Add(o Stats) {
	s.CallSites += o.CallSites
	s.Exact += o.Exact
	s.NameOnly += o.NameOnly
	s.Ambiguous += o.Ambiguous
	s.Unresolved += o.Unresolved
	for k, v := range o.Reasons {
		s.Reasons[k] += v
	}
	for k, v := range o.ByKind {
		s.ByKind[k] += v
	}
	s.SkippedBuiltin += o.SkippedBuiltin
	s.SkippedConversion += o.SkippedConversion
	s.DeferredLocal += o.DeferredLocal
	s.InterfacesPartial += o.InterfacesPartial
}

// Check reports whether the invariant holds: every unique call site is stored,
// skipped or deferred.
func (s Stats) Check() error {
	if got := s.Stored() + s.Skipped() + s.Deferred(); got != s.CallSites {
		return fmt.Errorf("call sites %d != stored %d + skipped %d + deferred %d = %d",
			s.CallSites, s.Stored(), s.Skipped(), s.Deferred(), got)
	}
	sum := 0
	for _, n := range s.Reasons {
		sum += n
	}
	if sum != s.Unresolved {
		return fmt.Errorf("unresolved reasons %d != unresolved rows %d", sum, s.Unresolved)
	}
	return nil
}
