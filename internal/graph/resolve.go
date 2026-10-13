package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

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

// NameIndex is the cross-package declared-name index (spec story 101): every
// indexed package's package-level declared names by kind, plus the set of
// indexed packages so a lookup can tell an indexed package (no_match or a
// conversion) from an external one. It is built by scanning the source of every
// indexed file once.
type NameIndex struct {
	byPkgName map[string]string
	packages  map[string]bool
}

// NewNameIndex returns an empty index.
func NewNameIndex() *NameIndex {
	return &NameIndex{byPkgName: map[string]string{}, packages: map[string]bool{}}
}

// Add records one file's package and its package-level declared names, marking
// the package indexed even when it declares nothing.
func (ni *NameIndex) Add(pkg string, declared map[string]string) {
	ni.packages[pkg] = true
	for name, kind := range declared {
		ni.byPkgName[pkgName(pkg, name)] = kind
	}
}

// Declared returns the kind of a package-level name ("type", "func", "var" or
// "const"), or "" when the package does not declare it.
func (ni *NameIndex) Declared(pkg, name string) string {
	return ni.byPkgName[pkgName(pkg, name)]
}

// IsIndexed reports whether pkg is an indexed package (has files).
func (ni *NameIndex) IsIndexed(pkg string) bool { return ni.packages[pkg] }

// Analyze classifies one file's call sites and returns the edges to store plus
// the stats for those call sites. names is the cross-package declared-name index
// (spec story 101).
func Analyze(path, pkg string, info FileInfo, ix *Index, names *NameIndex) ([]Edge, Stats) {
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

			switch {
			case c.Name != "":
				edges, st = analyzeIdentifier(edges, st, callerID, c, d, pkg, ix, names)
			case c.Qualifier != "":
				edges, st = analyzeQualified(edges, st, callerID, c, d, info.Aliases, ix, names)
			default:
				// A receiver-style selector or a chained call: deferred.
				st.DeferredSelector++
			}
		}
	}
	return edges, st
}

// analyzeIdentifier classifies a bare identifier call: local scope, same-package
// function (Rule 1), builtin or conversion, then no_match (spec story 102).
func analyzeIdentifier(edges []Edge, st Stats, callerID int64, c Call, d Decl, pkg string, ix *Index, names *NameIndex) ([]Edge, Stats) {
	if d.Locals[c.Name] {
		st.DeferredLocal++
		return edges, st
	}
	if id, ok := ix.SamePackageFunc(pkg, c.Name); ok {
		st.Exact++
		st.ByKind[KindFunc]++
		return append(edges, Edge{
			CallerID: callerID, CalleeID: id, CalleeRaw: c.Raw,
			Resolution: ResolutionExact, CallKind: KindFunc, CallLine: c.Line,
		}), st
	}
	if parse.IsBuiltin(c.Name) && names.Declared(pkg, c.Name) == "" {
		st.SkippedBuiltin++
		return edges, st
	}
	if names.Declared(pkg, c.Name) == "type" {
		st.SkippedConversion++
		return edges, st
	}
	// A missing identifier, or a package-level var/const used as a call
	// (a function value; a later slice reclassifies it).
	st.Unresolved++
	st.Reasons[ReasonNoMatch]++
	st.ByKind[KindUnknown]++
	return append(edges, Edge{
		CallerID: callerID, CalleeID: 0, CalleeRaw: c.Raw,
		Resolution: ResolutionUnresolved, CallKind: KindUnknown, CallLine: c.Line,
		UnresolvedReason: ReasonNoMatch,
	}), st
}

// analyzeQualified classifies an alias-qualified selector call `alias.Name`
// (Rule 2): a same-package function in the aliased package is exact, an
// unindexed import path is external, and an indexed package without a matching
// function is a conversion (declared type) or no_match. A shadowed alias or an
// unknown qualifier stays deferred.
func analyzeQualified(edges []Edge, st Stats, callerID int64, c Call, d Decl, aliases map[string]string, ix *Index, names *NameIndex) ([]Edge, Stats) {
	if d.Locals[c.Qualifier] {
		st.DeferredSelector++
		return edges, st
	}
	importPath, ok := aliases[c.Qualifier]
	if !ok {
		st.DeferredSelector++
		return edges, st
	}
	if id, ok := ix.SamePackageFunc(importPath, c.Field); ok {
		st.Exact++
		st.ByKind[KindFunc]++
		return append(edges, Edge{
			CallerID: callerID, CalleeID: id, CalleeRaw: c.Raw,
			Resolution: ResolutionExact, CallKind: KindFunc, CallLine: c.Line,
		}), st
	}
	if !names.IsIndexed(importPath) {
		st.Unresolved++
		st.Reasons[ReasonExternal]++
		st.ByKind[KindUnknown]++
		return append(edges, Edge{
			CallerID: callerID, CalleeID: 0, CalleeRaw: c.Raw,
			Resolution: ResolutionUnresolved, CallKind: KindUnknown, CallLine: c.Line,
			UnresolvedReason: ReasonExternal,
		}), st
	}
	if names.Declared(importPath, c.Field) == "type" {
		st.SkippedConversion++
		return edges, st
	}
	st.Unresolved++
	st.Reasons[ReasonNoMatch]++
	st.ByKind[KindUnknown]++
	return append(edges, Edge{
		CallerID: callerID, CalleeID: 0, CalleeRaw: c.Raw,
		Resolution: ResolutionUnresolved, CallKind: KindUnknown, CallLine: c.Line,
		UnresolvedReason: ReasonNoMatch,
	}), st
}

// Fingerprint is the freshness fingerprint (D-076, amended to cover declared
// names): sha256 over the indexed symbols (id, kind, package, qualified name,
// receiver) ordered by id, the declared-name index (package -> name -> kind)
// ordered deterministically, and the resolver version. Line numbers and body
// hashes are excluded, so a body-only edit does not invalidate the graph. The
// declared-name part is included because Rule 2 and the builtin/conversion
// classification depend on package-level declared names that are not stored as
// symbols (for example `type Foo int`).
func Fingerprint(symbols []Symbol, names *NameIndex, resolverVersion int) string {
	h := sha256.New()
	fmt.Fprintf(h, "resolver:%d\n", resolverVersion)
	for _, s := range symbols {
		fmt.Fprintf(h, "%d\x00%s\x00%s\x00%s\x00%s\n", s.ID, s.Kind, s.Package, s.QualifiedName, s.Receiver)
	}
	if names != nil {
		fmt.Fprintf(h, "%s\n", names.digest())
	}
	return hex.EncodeToString(h.Sum(nil))
}

// digest renders the declared-name index deterministically: packages sorted by
// import path, each package's declared names sorted, with each name's kind.
func (ni *NameIndex) digest() string {
	byPkg := make(map[string][]string, len(ni.packages))
	for p := range ni.packages {
		byPkg[p] = nil
	}
	for k, kind := range ni.byPkgName {
		i := strings.IndexByte(k, 0)
		if i < 0 {
			continue
		}
		pkg, name := k[:i], k[i+1:]
		byPkg[pkg] = append(byPkg[pkg], name+"\x00"+kind)
	}
	pkgs := make([]string, 0, len(byPkg))
	for p := range byPkg {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)

	var b strings.Builder
	for _, p := range pkgs {
		decls := byPkg[p]
		sort.Strings(decls)
		fmt.Fprintf(&b, "pkg\x00%s\n", p)
		for _, d := range decls {
			fmt.Fprintf(&b, "decl\x00%s\x00%s\n", p, d)
		}
	}
	return b.String()
}

type callLine struct {
	raw  string
	line int
}

func pkgName(pkg, name string) string { return pkg + "\x00" + name }

func pathNameRecv(path, name, receiver string) string {
	return path + "\x00" + name + "\x00" + receiver
}
