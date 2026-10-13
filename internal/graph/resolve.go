package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
)

// Resolution values (D-073).
const (
	ResolutionExact      = "exact"
	ResolutionNameOnly   = "name_only"
	ResolutionAmbiguous  = "ambiguous"
	ResolutionUnresolved = "unresolved"
)

// Unresolved reasons (D-073).
const (
	ReasonExternal        = "external"
	ReasonNoMatch         = "no_match"
	ReasonUnknownReceiver = "unknown_receiver"
	ReasonFuncValue       = "func_value"
	ReasonCandidatesCap   = "candidates_capped"
)

// Call kinds.
const (
	KindFunc            = "func"
	KindConcreteMethod  = "concrete_method"
	KindInterfaceMethod = "interface_method"
	KindFuncValue       = "func_value"
	KindUnknown         = "unknown"
)

// Symbol is one row of the symbol index the resolver matches against.
type Symbol struct {
	ID            int64
	FileID        int64
	Path          string
	Package       string
	Name          string
	QualifiedName string
	Receiver      string
	Kind          string
	StartLine     int
}

// Edge is one stored edge. CalleeID is 0 for an unresolved edge, which then
// carries an UnresolvedReason.
type Edge struct {
	CallerID         int64
	CalleeID         int64
	CalleeRaw        string
	Resolution       string
	CallKind         string
	CallLine         int
	UnresolvedReason string
}

// Index is the in-memory symbol index, built once per graph run (spec story 64).
type Index struct {
	byFileLine     map[fileLine]int64
	byPathNameRecv map[string]int64
	funcsByPkgName map[string]int64
}

type fileLine struct {
	path string
	line int
}

// NewIndex builds the lookup tables from symbols. Pass symbols ordered by id so
// same-name duplicates resolve to the lowest id deterministically.
func NewIndex(symbols []Symbol) *Index {
	ix := &Index{
		byFileLine:     make(map[fileLine]int64, len(symbols)),
		byPathNameRecv: make(map[string]int64, len(symbols)),
		funcsByPkgName: make(map[string]int64, len(symbols)),
	}
	for _, s := range symbols {
		ix.byFileLine[fileLine{s.Path, s.StartLine}] = s.ID
		ix.byPathNameRecv[pathNameRecv(s.Path, s.Name, s.Receiver)] = s.ID
		if s.Kind == parse.KindFunc {
			k := pkgName(s.Package, s.Name)
			if _, ok := ix.funcsByPkgName[k]; !ok {
				ix.funcsByPkgName[k] = s.ID
			}
		}
	}
	return ix
}

// CallerID returns the symbol id of the declaration at path/startLine, falling
// back to (path, name, receiver) when the line does not match.
func (ix *Index) CallerID(path, name, receiver string, startLine int) (int64, bool) {
	if id, ok := ix.byFileLine[fileLine{path, startLine}]; ok {
		return id, true
	}
	id, ok := ix.byPathNameRecv[pathNameRecv(path, name, receiver)]
	return id, ok
}

// SamePackageFunc returns the id of the same-package function named name
// (Rule 1).
func (ix *Index) SamePackageFunc(pkg, name string) (int64, bool) {
	id, ok := ix.funcsByPkgName[pkgName(pkg, name)]
	return id, ok
}

// Analyze classifies one file's call sites (the plain-call subset) and returns the
// edges to store plus the stats for those call sites. declared is the
// package-level declared-name index of the package being graphed.
func Analyze(path, pkg string, info FileInfo, ix *Index, declared map[string]string) ([]Edge, Stats) {
	st := NewStats()
	var edges []Edge
	for _, d := range info.Decls {
		callerID, ok := ix.CallerID(path, d.Name, d.Receiver, d.StartLine)
		if !ok {
			continue
		}
		seen := make(map[callLine]bool)
		for _, c := range d.Calls {
			key := callLine{c.Raw, c.Line}
			if seen[key] {
				continue
			}
			seen[key] = true
			st.CallSites++

			if c.Name == "" {
				st.DeferredSelector++
				continue
			}
			if d.Locals[c.Name] {
				st.DeferredLocal++
				continue
			}
			if id, ok := ix.SamePackageFunc(pkg, c.Name); ok {
				edges = append(edges, Edge{
					CallerID: callerID, CalleeID: id, CalleeRaw: c.Raw,
					Resolution: ResolutionExact, CallKind: KindFunc, CallLine: c.Line,
				})
				st.Exact++
				st.ByKind[KindFunc]++
				continue
			}
			if parse.IsBuiltin(c.Name) && declared[c.Name] == "" {
				st.SkippedBuiltin++
				continue
			}
			if declared[c.Name] == "type" {
				st.SkippedConversion++
				continue
			}
			// A missing identifier, or a package-level var/const used as a call
			// (a function value; a later slice reclassifies it).
			edges = append(edges, Edge{
				CallerID: callerID, CalleeID: 0, CalleeRaw: c.Raw,
				Resolution: ResolutionUnresolved, CallKind: KindUnknown, CallLine: c.Line,
				UnresolvedReason: ReasonNoMatch,
			})
			st.Unresolved++
			st.Reasons[ReasonNoMatch]++
			st.ByKind[KindUnknown]++
		}
	}
	return edges, st
}

// Fingerprint is the freshness fingerprint (D-076): sha256 over
// (id, kind, package, qualified name, receiver) of every indexed symbol ordered
// by id, plus the resolver version. Line numbers and body hashes are excluded,
// so a body-only edit does not invalidate the graph.
func Fingerprint(symbols []Symbol, resolverVersion int) string {
	h := sha256.New()
	fmt.Fprintf(h, "resolver:%d\n", resolverVersion)
	for _, s := range symbols {
		fmt.Fprintf(h, "%d\x00%s\x00%s\x00%s\x00%s\n", s.ID, s.Kind, s.Package, s.QualifiedName, s.Receiver)
	}
	return hex.EncodeToString(h.Sum(nil))
}

type callLine struct {
	raw  string
	line int
}

func pkgName(pkg, name string) string { return pkg + "\x00" + name }

func pathNameRecv(path, name, receiver string) string {
	return path + "\x00" + name + "\x00" + receiver
}
