package graph

import "fmt"

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
	DeferredLocal     int
	DeferredInterface int
	// InterfacesPartial counts interfaces with an unresolvable embedded
	// interface (Rule 4, 0 until interface resolution lands).
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
	return s.DeferredLocal + s.DeferredInterface
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
	s.DeferredInterface += o.DeferredInterface
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
