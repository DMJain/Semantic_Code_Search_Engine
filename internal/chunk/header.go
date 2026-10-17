package chunk

import (
	"sort"
	"strings"
)

// HeaderListMax is the number of callees and callers a full context header
// lists, the top of each side by rank (D-081).
const HeaderListMax = 5

// The edge resolutions a header may read. Only tree-sitter edges are used, and
// ambiguous rows are never a callee or caller in a header (D-081).
const (
	ResolutionExact     = "exact"
	ResolutionNameOnly  = "name_only"
	ResolutionAmbiguous = "ambiguous"
)

// HeaderInput is everything one context header renders from: the chunk's
// package import path, repo-root-relative file path, the symbol signature, and
// the ranked callee and caller short names (already limited to five by the
// caller, but capped here too).
type HeaderInput struct {
	Package   string
	File      string
	Signature string
	Callees   []string
	Callers   []string
}

// CallRef is one tree-sitter edge candidate for a header list, carrying the
// keys the ranking needs: the display name comes from the qualified name, the
// tie-breaks from the symbol name, qualified name and id (D-081).
type CallRef struct {
	Name          string
	QualifiedName string
	ID            int64
	Resolution    string
}

// ShortName renders a stored qualified name as the header shows it: a method
// keeps its receiver base with the leading star and the package path dropped
// (`...(*Scheduler).schedulePod` -> `Scheduler.schedulePod`), a func, struct or
// interface keeps its plain name (`...Target` -> `Target`).
func ShortName(qualified string) string {
	if i := strings.Index(qualified, ".("); i >= 0 {
		rest := qualified[i+2:]
		if j := strings.Index(rest, ")."); j >= 0 {
			recv := strings.TrimPrefix(rest[:j], "*")
			return recv + "." + rest[j+2:]
		}
	}
	if i := strings.LastIndexByte(qualified, '.'); i >= 0 {
		return qualified[i+1:]
	}
	return qualified
}

// TopNames ranks edge candidates (D-081) and returns the short names of the top
// five: exact before name_only, ambiguous excluded, then name ascending, with
// qualified name and then id as final deterministic tie-breaks.
func TopNames(refs []CallRef) []string {
	filtered := make([]CallRef, 0, len(refs))
	for _, r := range refs {
		if r.Resolution == ResolutionExact || r.Resolution == ResolutionNameOnly {
			filtered = append(filtered, r)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool { return lessCall(filtered[i], filtered[j]) })
	if len(filtered) > HeaderListMax {
		filtered = filtered[:HeaderListMax]
	}
	out := make([]string, len(filtered))
	for i, r := range filtered {
		out[i] = ShortName(r.QualifiedName)
	}
	return out
}

// lessCall orders two candidates by the header ranking rule.
func lessCall(a, b CallRef) bool {
	if ra, rb := resolutionRank(a.Resolution), resolutionRank(b.Resolution); ra != rb {
		return ra < rb
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	if a.QualifiedName != b.QualifiedName {
		return a.QualifiedName < b.QualifiedName
	}
	return a.ID < b.ID
}

func resolutionRank(r string) int {
	if r == ResolutionExact {
		return 0
	}
	return 1
}

// BuildHeader renders the full context header and degrades it to fit the header
// budget for a chunk of contentTokens tokens: RequestBudget less the content and
// the largest document prefix (D-082). It returns the header and the count of
// dropped items (0 = full; each dropped list item plus one for a dropped
// signature line). Package and file lines are never dropped.
func BuildHeader(in HeaderInput, contentTokens int) (string, int) {
	return buildHeader(in, RequestBudget-contentTokens-MaxDocPrefixTokens)
}

// buildHeader is BuildHeader with an explicit budget, so each degradation stage
// is testable at a chosen budget.
func buildHeader(in HeaderInput, budget int) (string, int) {
	callees := capList(in.Callees)
	callers := capList(in.Callers)
	dropSig := false
	render := func() string {
		return renderHeader(in.Package, in.File, in.Signature, callees, callers, dropSig)
	}

	// A non-positive budget leaves the two never-dropped lines only.
	if budget <= 0 {
		return renderPkgFile(in.Package, in.File), len(callers) + len(callees) + 1
	}

	degraded := 0
	if h := render(); EstimateTokens(h) <= budget {
		return h, 0
	}
	// Drop callers rank 5 down to rank 1.
	for i := len(callers) - 1; i >= 0; i-- {
		callers = callers[:i]
		degraded++
		if h := render(); EstimateTokens(h) <= budget {
			return h, degraded
		}
	}
	// Then callees rank 5 down to rank 1.
	for i := len(callees) - 1; i >= 0; i-- {
		callees = callees[:i]
		degraded++
		if h := render(); EstimateTokens(h) <= budget {
			return h, degraded
		}
	}
	// Then the whole signature line.
	dropSig = true
	degraded++
	if h := render(); EstimateTokens(h) <= budget {
		return h, degraded
	}
	// Nothing but the package and file lines can fit.
	return renderPkgFile(in.Package, in.File), degraded
}

// renderPkgFile renders just the two lines that are never dropped.
func renderPkgFile(pkg, file string) string {
	return "// package: " + pkg + "\n// file: " + file
}

// renderHeader renders the five header lines; a dropped signature line is
// omitted, and an empty list still prints its line with none (D-081).
func renderHeader(pkg, file, sig string, callees, callers []string, dropSig bool) string {
	lines := []string{
		"// package: " + pkg,
		"// file: " + file,
	}
	if !dropSig {
		lines = append(lines, "// signature: "+sig)
	}
	lines = append(lines,
		"// calls: "+joinOrNone(callees),
		"// called by: "+joinOrNone(callers),
	)
	return strings.Join(lines, "\n")
}

func joinOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func capList(names []string) []string {
	if len(names) > HeaderListMax {
		return names[:HeaderListMax]
	}
	return names
}
