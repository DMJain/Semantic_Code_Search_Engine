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
	// methodsByType maps (package, receiver base type, method name) to the
	// method symbol id, for Rule 3. The base type strips a leading `*`, so a
	// value call resolves to a pointer-receiver method and vice versa (Go
	// auto-addresses).
	methodsByType map[string]int64
	// interfaces is the set of (package, name) named interface types, so a
	// known interface receiver is interface_method rather than a concrete one.
	interfaces map[string]bool
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
		methodsByType:  make(map[string]int64, len(symbols)),
		interfaces:     make(map[string]bool, len(symbols)),
	}
	for _, s := range symbols {
		ix.byFileLine[fileLine{s.Path, s.StartLine}] = s.ID
		ix.byPathNameRecv[pathNameRecv(s.Path, s.Name, s.Receiver)] = s.ID
		switch s.Kind {
		case parse.KindFunc:
			k := pkgName(s.Package, s.Name)
			if _, ok := ix.funcsByPkgName[k]; !ok {
				ix.funcsByPkgName[k] = s.ID
			}
		case parse.KindMethod:
			k := methodKey(s.Package, strings.TrimPrefix(s.Receiver, "*"), s.Name)
			if _, ok := ix.methodsByType[k]; !ok {
				ix.methodsByType[k] = s.ID
			}
		case parse.KindInterface:
			ix.interfaces[pkgName(s.Package, s.Name)] = true
		}
	}
	return ix
}

// ConcreteMethod returns the id of the method named method on the named type
// typ in pkg, accepting a value or pointer receiver (Rule 3, Go auto-addresses).
func (ix *Index) ConcreteMethod(pkg, typ, method string) (int64, bool) {
	id, ok := ix.methodsByType[methodKey(pkg, typ, method)]
	return id, ok
}

// IsInterface reports whether pkg declares a named interface type typ.
func (ix *Index) IsInterface(pkg, typ string) bool {
	return ix.interfaces[pkgName(pkg, typ)]
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
				edges, st = analyzeQualified(edges, st, callerID, c, d, pkg, info.Aliases, ix, names)
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

// analyzeQualified classifies a selector call `qualifier.Field` whose operand is
// a bare identifier. It is Rule 3 when the qualifier is a receiver or parameter
// with a written type, Rule 2 when it is an import alias (a same-package
// function in the aliased package is exact, an unindexed import path is
// external, and an indexed package without a matching function is a conversion
// or no_match), and otherwise a deferred selector. A qualifier shadowed by a
// local declaration is not a typed name and not an alias, so it stays deferred.
func analyzeQualified(edges []Edge, st Stats, callerID int64, c Call, d Decl, pkg string, aliases map[string]string, ix *Index, names *NameIndex) ([]Edge, Stats) {
	if typ, ok := d.Types[c.Qualifier]; ok {
		return analyzeReceiver(edges, st, callerID, c, typ, pkg, aliases, ix, names, d.TypeParams)
	}
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

// analyzeReceiver classifies a selector call on a name whose written type is
// known (Rule 3): the receiver of the enclosing method or a parameter. When the
// written type resolves to a named type the package actually declares, a method
// of that name with a matching receiver base (value or pointer; Go
// auto-addresses) is exact with kind concrete_method. A known interface type is
// counted deferred_interface until Rule 4 lands and never stored. Any other form
// (unknown, type parameter, no such method, embedded-only promotion) stays
// deferred_selector: types are never guessed.
func analyzeReceiver(edges []Edge, st Stats, callerID int64, c Call, typ, pkg string, aliases map[string]string, ix *Index, names *NameIndex, typeParams map[string]bool) ([]Edge, Stats) {
	targetPkg, typeName, ok := receiverType(typ, pkg, aliases, names, typeParams)
	if !ok {
		st.DeferredSelector++
		return edges, st
	}
	if ix.IsInterface(targetPkg, typeName) {
		st.DeferredInterface++
		return edges, st
	}
	if id, ok := ix.ConcreteMethod(targetPkg, typeName, c.Field); ok {
		st.Exact++
		st.ByKind[KindConcreteMethod]++
		return append(edges, Edge{
			CallerID: callerID, CalleeID: id, CalleeRaw: c.Raw,
			Resolution: ResolutionExact, CallKind: KindConcreteMethod, CallLine: c.Line,
		}), st
	}
	st.DeferredSelector++
	return edges, st
}

// receiverType resolves a written type text to a named type: a bare identifier
// is a type in the same package, `alias.Type` (where alias is a known import
// alias) is a type in the aliased package, and one leading `*` is stripped so a
// pointer and a value base resolve alike. The name must be a type the package
// actually declares (from the declared-name index), so an undeclared or
// dot-imported name is not guessed, and a type parameter name is never a type.
// Any other form (slice, map, func, generic instantiation, inline struct,
// qualified type through an unknown alias) is not a named type and returns
// ok=false. Builtin type names pass through as same-package names and simply
// find no declared type.
func receiverType(typ, pkg string, aliases map[string]string, names *NameIndex, typeParams map[string]bool) (string, string, bool) {
	t := strings.TrimSpace(typ)
	if strings.HasPrefix(t, "*") {
		t = strings.TrimSpace(t[1:])
	}
	if t == "" || strings.HasPrefix(t, "*") || strings.ContainsAny(t, "[](){} ,") {
		return "", "", false
	}
	if i := strings.LastIndexByte(t, '.'); i >= 0 {
		alias, name := t[:i], t[i+1:]
		if alias == "" || name == "" {
			return "", "", false
		}
		importPath, ok := aliases[alias]
		if !ok || !declaresType(names, importPath, name) {
			return "", "", false
		}
		return importPath, name, true
	}
	if typeParams[t] || !declaresType(names, pkg, t) {
		return "", "", false
	}
	return pkg, t, true
}

// declaresType reports whether the declared-name index knows name as a named
// type of pkg. A nil index declares nothing.
func declaresType(names *NameIndex, pkg, name string) bool {
	return names != nil && names.Declared(pkg, name) == "type"
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

func methodKey(pkg, typ, method string) string {
	return pkg + "\x00" + typ + "\x00" + method
}

func pathNameRecv(path, name, receiver string) string {
	return path + "\x00" + name + "\x00" + receiver
}
