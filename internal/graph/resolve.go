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
	// methodsByPkgName maps (package, method name) to every method symbol id of
	// that name in the package, in symbol-id order, for Rule 5.
	methodsByPkgName map[string][]int64
	// interfaces is the set of (package, name) named interface types, so a
	// known interface receiver is interface_method rather than a concrete one.
	interfaces map[string]bool
	// methodsOnType maps (package, receiver base) to the set of method names
	// declared on that type, for Rule 4 candidates. Interfaces cannot declare
	// methods, so this holds only concrete types.
	methodsOnType map[string]map[string]bool
	// typesWithMethod maps a method name to the concrete types that declare it,
	// in first-seen (symbol-id) order, so a candidate set is the intersection of
	// the interface's method names.
	typesWithMethod map[string][]TypeKey
}

// TypeKey identifies one concrete indexed type by its package (import path) and
// receiver base name (a leading `*` stripped).
type TypeKey struct {
	Pkg  string
	Type string
}

type fileLine struct {
	path string
	line int
}

// NewIndex builds the lookup tables from symbols. Pass symbols ordered by id so
// same-name duplicates resolve to the lowest id deterministically.
func NewIndex(symbols []Symbol) *Index {
	ix := &Index{
		byFileLine:       make(map[fileLine]int64, len(symbols)),
		byPathNameRecv:   make(map[string]int64, len(symbols)),
		funcsByPkgName:   make(map[string]int64, len(symbols)),
		methodsByType:    make(map[string]int64, len(symbols)),
		methodsByPkgName: make(map[string][]int64, len(symbols)),
		interfaces:       make(map[string]bool, len(symbols)),
		methodsOnType:    make(map[string]map[string]bool),
		typesWithMethod:  make(map[string][]TypeKey),
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
			base := strings.TrimPrefix(s.Receiver, "*")
			k := methodKey(s.Package, base, s.Name)
			if _, ok := ix.methodsByType[k]; !ok {
				ix.methodsByType[k] = s.ID
			}
			tk := pkgName(s.Package, base)
			if ix.methodsOnType[tk] == nil {
				ix.methodsOnType[tk] = map[string]bool{}
			}
			if !ix.methodsOnType[tk][s.Name] {
				ix.methodsOnType[tk][s.Name] = true
				ix.typesWithMethod[s.Name] = append(ix.typesWithMethod[s.Name],
					TypeKey{Pkg: s.Package, Type: base})
			}
			ix.methodsByPkgName[pkgName(s.Package, s.Name)] = append(
				ix.methodsByPkgName[pkgName(s.Package, s.Name)], s.ID)
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

// MethodsByName returns every method symbol id named name declared in pkg, in
// symbol-id order, for Rule 5 (an unknown receiver).
func (ix *Index) MethodsByName(pkg, name string) []int64 {
	return ix.methodsByPkgName[pkgName(pkg, name)]
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
	// fields maps (package, struct name, field name) to the field's resolved
	// type, for Rule 3 field chains within one package.
	fields map[string]TypeRef
	// results maps (package, function name) to the function's single declared
	// result resolved type, so a same-package constructor call can be typed.
	results map[string]TypeRef
	// interfaces maps (package, name) to a named interface's direct method names
	// and embedded elements, for Rule 4 method-name expansion (D-075).
	interfaces map[string]InterfaceFact
}

// NewNameIndex returns an empty index.
func NewNameIndex() *NameIndex {
	return &NameIndex{
		byPkgName:  map[string]string{},
		packages:   map[string]bool{},
		fields:     map[string]TypeRef{},
		results:    map[string]TypeRef{},
		interfaces: map[string]InterfaceFact{},
	}
}

// Add records one file's package and its package-level declared names, marking
// the package indexed even when it declares nothing.
func (ni *NameIndex) Add(pkg string, declared map[string]string) {
	ni.packages[pkg] = true
	for name, kind := range declared {
		ni.byPkgName[pkgName(pkg, name)] = kind
	}
}

// AddFields records one file's named struct fields, keyed "<struct>\x00<field>".
func (ni *NameIndex) AddFields(pkg string, fields map[string]TypeRef) {
	ni.packages[pkg] = true
	for key, ref := range fields {
		ni.fields[pkg+"\x00"+key] = ref
	}
}

// AddResults records one file's single-result functions by name.
func (ni *NameIndex) AddResults(pkg string, results map[string]TypeRef) {
	ni.packages[pkg] = true
	for name, ref := range results {
		ni.results[pkgName(pkg, name)] = ref
	}
}

// AddInterfaces records one file's named interfaces.
func (ni *NameIndex) AddInterfaces(pkg string, ifaces map[string]InterfaceFact) {
	ni.packages[pkg] = true
	for name, f := range ifaces {
		ni.interfaces[pkgName(pkg, name)] = f
	}
}

// InterfaceMethodNames returns the known method names of the named interface
// (pkg, name), expanding same-package embedded interfaces recursively with a
// cycle guard. An embedded interface in another package, or one the index
// cannot resolve, contributes no names and sets partial: the returned set is
// then a subset, so the Rule 4 candidate set built from it is a superset. ok is
// false when (pkg, name) is not a known interface.
func (ni *NameIndex) InterfaceMethodNames(pkg, name string) (names map[string]bool, partial bool, ok bool) {
	if _, known := ni.interfaces[pkgName(pkg, name)]; !known {
		return nil, false, false
	}
	names = map[string]bool{}
	partial = ni.expandInterface(pkg, name, names, map[string]bool{})
	return names, partial, true
}

// expandInterface looks up one named interface in pkg and merges it. A missing
// name is unresolvable (partial); a name already being visited is a cycle and
// contributes nothing.
func (ni *NameIndex) expandInterface(pkg, name string, names, visiting map[string]bool) bool {
	key := pkgName(pkg, name)
	if visiting[key] {
		return false
	}
	fact, ok := ni.interfaces[key]
	if !ok {
		return true
	}
	visiting[key] = true
	defer delete(visiting, key)
	return ni.mergeInterface(pkg, fact, names, visiting)
}

// mergeInterface adds a fact's direct method names and expands its embedded
// elements. Inline interfaces are expanded in place; a cross-package or unknown
// embed is partial.
func (ni *NameIndex) mergeInterface(pkg string, fact InterfaceFact, names, visiting map[string]bool) bool {
	partial := false
	for m := range fact.Methods {
		names[m] = true
	}
	for _, e := range fact.Embeds {
		switch {
		case e.Unknown:
			partial = true
		case e.Inline != nil:
			partial = ni.mergeInterface(pkg, *e.Inline, names, visiting) || partial
		case e.Pkg != "" && e.Pkg != pkg:
			partial = true
		default:
			partial = ni.expandInterface(pkg, e.Name, names, visiting) || partial
		}
	}
	return partial
}

// Declared returns the kind of a package-level name ("type", "func", "var" or
// "const"), or "" when the package does not declare it.
func (ni *NameIndex) Declared(pkg, name string) string {
	return ni.byPkgName[pkgName(pkg, name)]
}

// FieldType returns the resolved type of structName's field in pkg, if the index
// records it.
func (ni *NameIndex) FieldType(pkg, structName, field string) (TypeRef, bool) {
	ref, ok := ni.fields[pkg+"\x00"+structName+"\x00"+field]
	return ref, ok
}

// FunctionResult returns the single declared result type of a same-package
// function, if the index records one.
func (ni *NameIndex) FunctionResult(pkg, name string) (TypeRef, bool) {
	ref, ok := ni.results[pkgName(pkg, name)]
	return ref, ok
}

// IsIndexed reports whether pkg is an indexed package (has files).
func (ni *NameIndex) IsIndexed(pkg string) bool { return ni.packages[pkg] }

// Analyze classifies one file's call sites and returns the edges to store plus
// the stats for those call sites. names is the cross-package declared-name index
// (spec story 101). An optional Rule4 configures interface-receiver handling;
// the default is production (capped at MaxInterfaceCandidates).
func Analyze(path, pkg string, info FileInfo, ix *Index, names *NameIndex, rule4 ...Rule4) ([]Edge, Stats) {
	r4 := Rule4{N: MaxInterfaceCandidates}
	if len(rule4) > 0 {
		r4 = rule4[0]
	}
	st := NewStats()
	var edges []Edge
	for _, d := range info.Decls {
		callerID, ok := ix.CallerID(path, d.Name, d.Receiver, d.StartLine)
		if !ok {
			continue
		}
		// Resolve constructor locals (`x := NewT(...)`) to their constructor's
		// single declared result type, a package-level fact (spec story 39). The
		// result carries the declaring file's package path, so a caller file
		// that binds the same alias to a different package cannot redirect it.
		ctors := make(map[string]TypeRef, len(d.ConstructorLocals))
		for name, ctor := range d.ConstructorLocals {
			if ref, ok := names.FunctionResult(pkg, ctor); ok {
				ctors[name] = ref
			}
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
				edges, st = analyzeSelector(edges, st, callerID, c, d, ctors, pkg, info.Aliases, ix, names, r4)
			default:
				// A selector on a call result (a method chain) or a call on a
				// call result: the receiver is unknown, so Rule 5 (spec story 56).
				edges, st = analyzeUnknownReceiver(edges, st, callerID, c, pkg, ix)
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

// analyzeSelector classifies a selector call whose operand is a bare identifier.
// It is Rule 3 when the qualifier is a receiver, parameter or inferable local
// with a written type and the (possibly field-chained) type resolves to a named
// type with that method; a known interface type is handled by Rule 4.
// It is Rule 2 when the qualifier is an import alias. Anything else (an unknown
// type, a shadowed local, an embedded-only promotion, a type without that
// method) has an unknown receiver: Rule 5 (spec story 56).
func analyzeSelector(edges []Edge, st Stats, callerID int64, c Call, d Decl, ctors map[string]TypeRef, pkg string, aliases map[string]string, ix *Index, names *NameIndex, r4 Rule4) ([]Edge, Stats) {
	if base, ok := baseType(c.Qualifier, d, ctors, pkg, aliases, names); ok {
		if targetPkg, typeName, ok := resolveChain(base, c.Fields, pkg, names); ok {
			if ix.IsInterface(targetPkg, typeName) {
				return analyzeInterface(edges, st, callerID, c, pkg, targetPkg, typeName, ix, names, r4)
			}
			if id, ok := ix.ConcreteMethod(targetPkg, typeName, c.Field); ok {
				st.Exact++
				st.ByKind[KindConcreteMethod]++
				return append(edges, Edge{
					CallerID: callerID, CalleeID: id, CalleeRaw: c.Raw,
					Resolution: ResolutionExact, CallKind: KindConcreteMethod, CallLine: c.Line,
				}), st
			}
		}
		return analyzeUnknownReceiver(edges, st, callerID, c, pkg, ix)
	}
	// Rule 2: an import alias, unless a local declaration shadows it (local scope
	// wins, so the receiver is unknown and Rule 5 applies).
	if importPath, ok := aliases[c.Qualifier]; ok && !d.Locals[c.Qualifier] {
		return analyzeAlias(edges, st, callerID, c, importPath, ix, names)
	}
	return analyzeUnknownReceiver(edges, st, callerID, c, pkg, ix)
}

// baseType returns the base type reference of a selector's qualifier: a written
// type from the function's type environment (a receiver, parameter or inferred
// local, resolved against the caller file's aliases), or a constructor result
// already resolved against the declaring file's aliases.
func baseType(name string, d Decl, ctors map[string]TypeRef, pkg string, aliases map[string]string, names *NameIndex) (TypeRef, bool) {
	if raw, ok := d.Types[name]; ok {
		return resolveWrittenType(raw, pkg, aliases, names, d.TypeParams)
	}
	if ref, ok := ctors[name]; ok {
		return ref, true
	}
	return TypeRef{}, false
}

// analyzeAlias classifies an alias-qualified call (Rule 2): a same-package
// function in the aliased package is exact; an import path with no indexed file
// is external; an indexed package without a matching function is a declared-type
// conversion (skipped_conversion) or no_match.
func analyzeAlias(edges []Edge, st Stats, callerID int64, c Call, importPath string, ix *Index, names *NameIndex) ([]Edge, Stats) {
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

// analyzeUnknownReceiver classifies a selector call whose receiver is still
// unknown (Rule 5, spec story 56): every method of the called name in the
// caller's package is looked up. Exactly one is stored name_only (a guess that
// always has a callee); several are stored as one ambiguous row per candidate
// with identical raw text; none is unresolved with reason unknown_receiver.
// Cross-package widening is deliberately not done.
func analyzeUnknownReceiver(edges []Edge, st Stats, callerID int64, c Call, pkg string, ix *Index) ([]Edge, Stats) {
	cands := ix.MethodsByName(pkg, c.Field)
	switch len(cands) {
	case 0:
		st.Unresolved++
		st.Reasons[ReasonUnknownReceiver]++
		st.ByKind[KindUnknown]++
		return append(edges, Edge{
			CallerID: callerID, CalleeID: 0, CalleeRaw: c.Raw,
			Resolution: ResolutionUnresolved, CallKind: KindUnknown, CallLine: c.Line,
			UnresolvedReason: ReasonUnknownReceiver,
		}), st
	case 1:
		st.NameOnly++
		st.ByKind[KindUnknown]++
		return append(edges, Edge{
			CallerID: callerID, CalleeID: cands[0], CalleeRaw: c.Raw,
			Resolution: ResolutionNameOnly, CallKind: KindUnknown, CallLine: c.Line,
		}), st
	default:
		for _, id := range cands {
			edges = append(edges, Edge{
				CallerID: callerID, CalleeID: id, CalleeRaw: c.Raw,
				Resolution: ResolutionAmbiguous, CallKind: KindUnknown, CallLine: c.Line,
			})
		}
		st.Ambiguous += len(cands)
		st.ByKind[KindUnknown] += len(cands)
		// An ambiguous call site stores one row per candidate; each row is a
		// stored edge, so it contributes one call unit to the invariant.
		st.CallSites += len(cands) - 1
		return edges, st
	}
}

// resolveChain resolves a base type reference followed by a chain of struct
// field names to a final named type (a concrete package path and name), for
// Rule 3 field chains within one package (`s.cache.Assume`). Field types carry
// the package path resolved in the declaring file, so a caller file that binds
// the same alias to a different package cannot redirect a step. A base that is
// not a named type, a field chain that leaves the package, or a field whose type
// is unknown or embedded (promotion is unseen) returns ok=false.
func resolveChain(base TypeRef, fields []string, pkg string, names *NameIndex) (string, string, bool) {
	targetPkg, typeName := refPkg(base.Pkg, pkg), base.Name
	if typeName == "" || !declaresType(names, targetPkg, typeName) {
		return "", "", false
	}
	for _, field := range fields {
		if targetPkg != pkg {
			return "", "", false
		}
		ref, ok := names.FieldType(pkg, typeName, field)
		if !ok {
			return "", "", false
		}
		targetPkg, typeName = refPkg(ref.Pkg, pkg), ref.Name
		if typeName == "" || !declaresType(names, targetPkg, typeName) {
			return "", "", false
		}
	}
	return targetPkg, typeName, true
}

// refPkg returns a concrete package path for a resolved type reference: the
// stored path, or fallback when the reference is same-package (Pkg "").
func refPkg(pkg, fallback string) string {
	if pkg == "" {
		return fallback
	}
	return pkg
}

// resolveWrittenType resolves a written type text (a receiver, parameter or
// inferred-local type from the same file) to a TypeRef against the caller file's
// aliases, normalising a same-package type to an empty Pkg.
func resolveWrittenType(raw, pkg string, aliases map[string]string, names *NameIndex, typeParams map[string]bool) (TypeRef, bool) {
	target, name, ok := receiverType(raw, pkg, aliases, names, typeParams)
	if !ok {
		return TypeRef{}, false
	}
	if target == pkg {
		target = ""
	}
	return TypeRef{Pkg: target, Name: name}, true
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

// digest renders the resolver index deterministically: declared names (with
// kind), struct fields (with resolved type) and function single results (with
// resolved type), each package's entries sorted. It is order-independent so the
// same facts inserted in any order give the same fingerprint.
func (ni *NameIndex) digest() string {
	var b strings.Builder
	{
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
		writeDigest(&b, "decl", byPkg)
	}
	{
		byPkg := map[string][]string{}
		for k, ref := range ni.fields {
			i := strings.IndexByte(k, 0)
			if i < 0 {
				continue
			}
			pkg, rest := k[:i], k[i+1:]
			byPkg[pkg] = append(byPkg[pkg], rest+"\x00"+ref.Pkg+"\x00"+ref.Name)
		}
		writeDigest(&b, "field", byPkg)
	}
	{
		byPkg := map[string][]string{}
		for k, ref := range ni.results {
			i := strings.IndexByte(k, 0)
			if i < 0 {
				continue
			}
			pkg, name := k[:i], k[i+1:]
			byPkg[pkg] = append(byPkg[pkg], name+"\x00"+ref.Pkg+"\x00"+ref.Name)
		}
		writeDigest(&b, "result", byPkg)
	}
	{
		byPkg := map[string][]string{}
		for k, fact := range ni.interfaces {
			i := strings.IndexByte(k, 0)
			if i < 0 {
				continue
			}
			pkg, name := k[:i], k[i+1:]
			entries := fact.digestEntries()
			sort.Strings(entries)
			byPkg[pkg] = append(byPkg[pkg], name+"\x00"+strings.Join(entries, "|"))
		}
		writeDigest(&b, "iface", byPkg)
	}
	return b.String()
}

// digestEntries renders one interface fact deterministically: a direct method,
// an embed, an inline interface (recursed) or an unresolvable element. The
// order is normalised by the caller.
func (f InterfaceFact) digestEntries() []string {
	var out []string
	for m := range f.Methods {
		out = append(out, "m\x00"+m)
	}
	for _, e := range f.Embeds {
		switch {
		case e.Unknown:
			out = append(out, "u")
		case e.Inline != nil:
			out = append(out, "i\x00"+strings.Join(e.Inline.digestEntries(), "|"))
		default:
			out = append(out, "e\x00"+e.Pkg+"\x00"+e.Name)
		}
	}
	sort.Strings(out)
	return out
}

// writeDigest renders one section (tag) of package keyed entries, packages and
// entries sorted, so the output is deterministic.
func writeDigest(b *strings.Builder, tag string, byPkg map[string][]string) {
	pkgs := make([]string, 0, len(byPkg))
	for p := range byPkg {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	for _, p := range pkgs {
		entries := byPkg[p]
		sort.Strings(entries)
		fmt.Fprintf(b, "pkg\x00%s\n", p)
		for _, e := range entries {
			fmt.Fprintf(b, "%s\x00%s\x00%s\n", tag, p, e)
		}
	}
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
