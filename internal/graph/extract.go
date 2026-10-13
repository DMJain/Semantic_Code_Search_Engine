package graph

import (
	"context"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
)

// Decl is one declared function or method and the call sites in its body.
// Calls inside function literals are collected here too, attributed to this
// declaration (spec story 42): the walk never starts a new caller for a literal.
type Decl struct {
	Name      string
	Receiver  string
	StartLine int
	// Locals is the flat local scope of the declaration: the receiver name,
	// parameters, and every :=, var or const name in the body (function-literal
	// variables included). A call to a name in this set is deferred_local.
	Locals map[string]bool
	// Types maps the receiver name, every parameter name and every local whose
	// written type is inferable (a `var x T`, a `var x = T{...}`, an `x :=
	// T{...}` or an `x := &T{...}`) to its written type text (for example
	// `*Scheduler`, `Interface`, `p *pkg.T`, `f func()`). It is the per-function
	// type environment Rule 3 resolves against. A name redeclared anywhere in the
	// body is removed, so a shadowed name is unknown (an inner variable never
	// inherits the outer type). Unknown forms are kept as written and rejected by
	// the resolver, which never guesses.
	Types map[string]string
	// ConstructorLocals maps a locally declared name to the same-package
	// constructor call it was initialised from (`x := NewT(...)`), whose single
	// declared result type becomes the name's type. The result type is a
	// package-level fact resolved at Analyze time through the NameIndex.
	ConstructorLocals map[string]string
	// TypeParams is the set of the declaration's own type parameter names. A
	// receiver or parameter typed as one of them is unknown, so a type parameter
	// that shares a name with a package type is never resolved to that type.
	TypeParams map[string]bool
	Calls      []Call
}

// Call is one call expression. Name is the identifier for a bare identifier
// call. Qualifier and Field are set for a selector call whose operand is a bare
// identifier (the alias-qualified `alias.Name` shape). Fields holds the
// intermediate selector names of a field chain between the base identifier and
// the called field (`s.cache.Assume` gives Fields [cache], Field Assume), so the
// resolver can walk the receiver's struct fields. When the selector's operand is
// itself a call (`a.B().C()`) the base is unknown and Qualifier is empty while
// Field is still the called name. Raw is set for every call; a call with an empty
// Field is a call on a call result and is an unknown receiver.
type Call struct {
	Raw       string
	Name      string
	Qualifier string
	Field     string
	Fields    []string
	Line      int
}

// FileInfo is the extraction result for one file: its declared functions and
// methods, its package-level declared names, and its import alias map. Declared
// maps a name to its kind ("type", "func", "var" or "const") so the resolver can
// tell a type conversion from a call through a package-level function variable.
// Aliases maps a file import alias to its import path (dot and blank imports are
// ignored).
type FileInfo struct {
	Decls    []Decl
	Declared map[string]string
	Aliases  map[string]string
}

// Extract re-parses src and returns its declared functions/methods with their
// call sites, its package-level declared names and its import alias map (spec
// stories 38, 101). Each declaration carries the receiver, parameter and
// inferable local types as written (the Rule 3 type environment); constructor
// calls keep the constructor name for the resolver to resolve through the
// package's single-result facts.
func Extract(src []byte) (FileInfo, error) {
	tree, err := parseTree(src)
	if err != nil {
		return FileInfo{}, err
	}
	defer tree.Close()
	root := tree.RootNode()

	info := FileInfo{Declared: map[string]string{}, Aliases: map[string]string{}}
	collectDeclaredNames(root, src, info.Declared)
	collectImports(root, src, info.Aliases)
	for i := 0; i < int(root.NamedChildCount()); i++ {
		switch n := root.NamedChild(i); n.Type() {
		case "function_declaration", "method_declaration":
			info.Decls = append(info.Decls, extractDecl(n, src))
		}
	}
	return info, nil
}

// FileFacts is the names-only tree-sitter result for one file (spec story 101
// TypeRef is a resolved type reference for a same-package fact (a struct field
// type or a constructor result). Pkg is the imported package path of the named
// type ("" means the declaring package); Name is the type name. A qualified type
// is resolved against the DECLARING file's import aliases at extraction time, so
// a caller file that binds the same alias to a different package cannot redirect
// the resolution (spec story 39).
type TypeRef struct {
	Pkg  string
	Name string
}

// FileFacts is the names-only tree-sitter result for one file (spec story 101
// and the same-package facts Rule 3 needs): the package-level declared names, the
// resolved type of every named field of each named struct (embedded fields are
// skipped, so promotion is unseen), and the single declared result type of each
// function (a function with zero or two-or-more results is not a constructor and
// is absent).
type FileFacts struct {
	// Declared maps a package-level name to its kind ("type", "func", "var" or
	// "const").
	Declared map[string]string
	// Fields maps "<struct>\x00<field>" to the field's resolved type.
	Fields map[string]TypeRef
	// Results maps a function name to its single declared result resolved type.
	Results map[string]TypeRef
}

// SourceFacts re-parses src and returns the package-level facts the resolver
// needs across files: declared names, struct fields and single-result
// constructors. Qualified field and result types are resolved against src's own
// import aliases. It is the names-only path used to build the cross-package
// NameIndex (spec story 101) without extracting call sites.
func SourceFacts(src []byte) (FileFacts, error) {
	tree, err := parseTree(src)
	if err != nil {
		return FileFacts{}, err
	}
	defer tree.Close()
	root := tree.RootNode()

	aliases := map[string]string{}
	collectImports(root, src, aliases)

	facts := FileFacts{
		Declared: map[string]string{},
		Fields:   map[string]TypeRef{},
		Results:  map[string]TypeRef{},
	}
	collectDeclaredNames(root, src, facts.Declared)
	collectStructFields(root, src, aliases, facts.Fields)
	collectFuncResults(root, src, aliases, facts.Results)
	return facts, nil
}

// collectDeclaredNames records the package-level declared names (functions,
// named types, vars and consts) of every top-level declaration.
func collectDeclaredNames(root *sitter.Node, src []byte, into map[string]string) {
	for i := 0; i < int(root.NamedChildCount()); i++ {
		n := root.NamedChild(i)
		switch n.Type() {
		case "function_declaration":
			if name := fieldName(n, src); name != "" {
				into[name] = "func"
			}
		case "type_declaration":
			collectTypeNames(n, src, into)
		case "var_declaration":
			collectSpecNames(n, src, "var", into)
		case "const_declaration":
			collectSpecNames(n, src, "const", into)
		}
	}
}

// collectImports records the file's import alias map: an explicit alias
// (`import a "p"`) or the last path segment of an unaliased import
// (`import "p"`). Dot and blank imports are ignored (spec story 38).
func collectImports(root *sitter.Node, src []byte, into map[string]string) {
	for i := 0; i < int(root.NamedChildCount()); i++ {
		n := root.NamedChild(i)
		if n.Type() != "import_declaration" {
			continue
		}
		for j := 0; j < int(n.NamedChildCount()); j++ {
			spec := n.NamedChild(j)
			switch spec.Type() {
			case "import_spec":
				addImport(spec, src, into)
			case "import_spec_list":
				for k := 0; k < int(spec.NamedChildCount()); k++ {
					if s := spec.NamedChild(k); s.Type() == "import_spec" {
						addImport(s, src, into)
					}
				}
			}
		}
	}
}

// addImport records one import_spec. An explicit alias wins; otherwise the alias
// is the last path segment. A dot or blank name is skipped.
func addImport(spec *sitter.Node, src []byte, into map[string]string) {
	pathNode := spec.ChildByFieldName("path")
	if pathNode == nil {
		return
	}
	importPath := strings.Trim(pathNode.Content(src), "\"`")
	if importPath == "" {
		return
	}
	name := ""
	if nameNode := spec.ChildByFieldName("name"); nameNode != nil {
		name = nameNode.Content(src)
	}
	switch name {
	case ".", "_":
		return
	case "":
		name = defaultAlias(importPath)
	}
	into[name] = importPath
}

// defaultAlias returns the package name Go binds for an unaliased import. It is
// the last path segment, except for a major-version module suffix `/vN` (N >= 2)
// whose package name is the parent segment: `k8s.io/klog/v2` binds `klog`, while
// `k8s.io/api/core/v1` (v1 is a real package name) keeps `v1`.
func defaultAlias(importPath string) string {
	base := importPath
	slash := strings.LastIndexByte(base, '/')
	if slash >= 0 {
		base = base[slash+1:]
	}
	if !isMajorVersion(base) || slash < 0 {
		return base
	}
	parent := importPath[:len(importPath)-len(base)-1]
	if i := strings.LastIndexByte(parent, '/'); i >= 0 {
		return parent[i+1:]
	}
	return parent
}

// isMajorVersion reports whether s is a major-version suffix `vN` with N >= 2
// (v1 and v1beta1/v1alpha1 are package names, not version suffixes).
func isMajorVersion(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	n := 0
	for _, c := range s[1:] {
		if c < '0' || c > '9' {
			return false
		}
		n = n*10 + int(c-'0')
	}
	return n >= 2
}

// parseTree parses src with tree-sitter and returns the tree; the caller
// closes it.
func parseTree(src []byte) (*sitter.Tree, error) {
	p := sitter.NewParser()
	defer p.Close()
	p.SetLanguage(golang.GetLanguage())

	return p.ParseCtx(context.Background(), nil, src)
}

// extractDecl builds one Decl from a function_declaration or method_declaration.
func extractDecl(n *sitter.Node, src []byte) Decl {
	d := Decl{
		Name:              fieldName(n, src),
		StartLine:         int(n.StartPoint().Row) + 1,
		Locals:            map[string]bool{},
		Types:             map[string]string{},
		ConstructorLocals: map[string]string{},
		TypeParams:        map[string]bool{},
	}
	addAll(d.TypeParams, typeParamNames(n.ChildByFieldName("type_parameters"), src))
	if recv := n.ChildByFieldName("receiver"); recv != nil {
		d.Receiver = receiverText(recv, src)
		if decl := recv.NamedChild(0); decl != nil {
			addAll(d.Locals, nameIdentifiers(decl, src))
			addType(d.Types, nameIdentifiers(decl, src), receiverText(recv, src))
		}
	}
	params := n.ChildByFieldName("parameters")
	addAll(d.Locals, paramNames(params, src))
	addParamTypes(d.Types, params, src)
	if body := n.ChildByFieldName("body"); body != nil {
		collectLocals(body, src, d.Locals)
		addInferredLocals(body, src, d)
		d.Calls = collectCalls(body, src)
	}
	return d
}

// addInferredLocals infers local variable types (spec story 39): a `var x T`,
// `var x = T{...}`, `x := T{...}` or `x := &T{...}` gives x the written type T,
// and an `x := NewT(...)` gives x the constructor name for the resolver to
// resolve through the package's single-result facts. A name redeclared in the
// body (twice locally, or shadowing the receiver or a parameter) is left
// unknown, matching the flat per-declaration scope: shadowing wins and a type is
// only ever turned into an unknown one, never invented.
func addInferredLocals(body *sitter.Node, src []byte, d Decl) {
	params := map[string]bool{}
	for name := range d.Types {
		params[name] = true
	}
	types := map[string]string{}
	ctors := map[string]string{}
	counts := map[string]int{}
	inferLocals(body, src, types, ctors, counts)
	removeShadowed(body, src, d.Types)
	for name, typ := range types {
		if counts[name] == 1 && !params[name] {
			d.Types[name] = typ
		}
	}
	for name, ctor := range ctors {
		// A local that shadows the constructor name means the call is a function
		// value, not the package function, so it is never a constructor.
		if counts[name] == 1 && !params[name] && !d.Locals[ctor] {
			d.ConstructorLocals[name] = ctor
		}
	}
}

// inferLocals walks body recording each local declaration's name count and, for
// a single-name declaration with an inferable initialiser, its written type or
// constructor name. Only short var declarations and var declarations are
// inferred; every other local form (range, receive, type-switch guard,
// function-literal parameter, const) is counted so a redeclaration still makes a
// name unknown.
func inferLocals(n *sitter.Node, src []byte, types, ctors map[string]string, counts map[string]int) {
	switch n.Type() {
	case "short_var_declaration":
		names := identList(n.ChildByFieldName("left"), src)
		for _, name := range names {
			counts[name]++
		}
		if len(names) == 1 {
			if v := singleExpr(n.ChildByFieldName("right")); v != nil {
				inferValue(names[0], v, src, types, ctors)
			}
		}
	case "var_declaration":
		inferVarSpecs(n, src, types, ctors, counts)
	case "const_declaration":
		countDeclNames(specNames(n, src), counts)
	case "range_clause":
		countDeclNames(identList(n.ChildByFieldName("left"), src), counts)
	case "receive_statement":
		countDeclNames(identList(n.ChildByFieldName("left"), src), counts)
	case "type_switch_statement":
		countDeclNames(identList(n.ChildByFieldName("alias"), src), counts)
	case "parameter_list":
		countDeclNames(paramNames(n, src), counts)
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		inferLocals(n.NamedChild(i), src, types, ctors, counts)
	}
}

// countDeclNames increments the declaration count of each name.
func countDeclNames(names []string, counts map[string]int) {
	for _, name := range names {
		counts[name]++
	}
}

// inferVarSpecs infers the types of every var_spec in a var_declaration,
// including a grouped var_spec_list.
func inferVarSpecs(decl *sitter.Node, src []byte, types, ctors map[string]string, counts map[string]int) {
	for i := 0; i < int(decl.NamedChildCount()); i++ {
		spec := decl.NamedChild(i)
		switch spec.Type() {
		case "var_spec":
			inferVarSpec(spec, src, types, ctors, counts)
		case "var_spec_list":
			for j := 0; j < int(spec.NamedChildCount()); j++ {
				if s := spec.NamedChild(j); s.Type() == "var_spec" {
					inferVarSpec(s, src, types, ctors, counts)
				}
			}
		}
	}
}

// inferVarSpec infers one var_spec: a written type gives every name that type; a
// single-name spec with an initialiser infers from the value.
func inferVarSpec(spec *sitter.Node, src []byte, types, ctors map[string]string, counts map[string]int) {
	names := nameIdentifiers(spec, src)
	countDeclNames(names, counts)
	if typ := spec.ChildByFieldName("type"); typ != nil {
		for _, name := range names {
			if name != "" {
				types[name] = typ.Content(src)
			}
		}
		return
	}
	if len(names) == 1 {
		if v := singleExpr(spec.ChildByFieldName("value")); v != nil {
			inferValue(names[0], v, src, types, ctors)
		}
	}
}

// inferValue infers a local's type from its initialiser: a composite literal
// gives its written type, a leading `&` is stripped (a pointer to a composite
// literal is the value type), and a bare same-package constructor call records
// the constructor name. Any other initialiser is unknown and never guessed.
func inferValue(name string, v *sitter.Node, src []byte, types, ctors map[string]string) {
	switch v.Type() {
	case "composite_literal":
		if t := v.ChildByFieldName("type"); t != nil {
			types[name] = t.Content(src)
		}
	case "unary_expression":
		if strings.HasPrefix(strings.TrimSpace(v.Content(src)), "&") {
			if operand := v.ChildByFieldName("operand"); operand != nil {
				inferValue(name, operand, src, types, ctors)
			}
		}
	case "call_expression":
		if fn := v.ChildByFieldName("function"); fn != nil && fn.Type() == "identifier" {
			ctors[name] = fn.Content(src)
		}
	}
}

// singleExpr returns the one expression of an expression_list, or nil when the
// list has any other shape (a multi-value initialiser is not inferred).
func singleExpr(list *sitter.Node) *sitter.Node {
	if list == nil || list.Type() != "expression_list" || list.NamedChildCount() != 1 {
		return nil
	}
	return list.NamedChild(0)
}

// addType maps each name to typ, skipping empty names and an empty type.
func addType(into map[string]string, names []string, typ string) {
	if typ == "" {
		return
	}
	for _, n := range names {
		if n != "" {
			into[n] = typ
		}
	}
}

// addParamTypes records each parameter's written type. A grouped `a, b T` gives
// both names the same type; an unnamed parameter contributes nothing.
func addParamTypes(into map[string]string, params *sitter.Node, src []byte) {
	if params == nil {
		return
	}
	for i := 0; i < int(params.NamedChildCount()); i++ {
		p := params.NamedChild(i)
		if p.Type() != "parameter_declaration" && p.Type() != "variadic_parameter_declaration" {
			continue
		}
		typ := p.ChildByFieldName("type")
		if typ == nil {
			continue
		}
		addType(into, nameIdentifiers(p, src), typ.Content(src))
	}
}

// removeShadowed deletes from types every name the body redeclares: a :=, var
// or const name, a range variable, a function-literal parameter, a type-switch
// guard or a select receive variable. The scope is flat per declaration,
// matching the tracer's flat local-scope simplification, so an inner declaration
// always wins; it can only turn a known name into an unknown one, never invent a
// type.
func removeShadowed(n *sitter.Node, src []byte, types map[string]string) {
	switch n.Type() {
	case "short_var_declaration":
		deleteAll(types, identList(n.ChildByFieldName("left"), src))
	case "var_declaration", "const_declaration":
		deleteAll(types, specNames(n, src))
	case "range_clause":
		deleteAll(types, identList(n.ChildByFieldName("left"), src))
	case "parameter_list":
		deleteAll(types, paramNames(n, src))
	case "type_switch_statement":
		deleteAll(types, identList(n.ChildByFieldName("alias"), src))
	case "receive_statement":
		deleteAll(types, identList(n.ChildByFieldName("left"), src))
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		removeShadowed(n.NamedChild(i), src, types)
	}
}

// deleteAll removes names from a set.
func deleteAll(set map[string]string, names []string) {
	for _, n := range names {
		delete(set, n)
	}
}

// collectCalls walks body (function literals included) in pre-order and returns
// every call expression. A chain is seen outer-first, matching raw-call order.
func collectCalls(body *sitter.Node, src []byte) []Call {
	var out []Call
	var visit func(n *sitter.Node)
	visit = func(n *sitter.Node) {
		if n.Type() == "call_expression" {
			if fn := n.ChildByFieldName("function"); fn != nil {
				if raw := strings.TrimSpace(fn.Content(src)); raw != "" {
					out = append(out, callShape(raw, fn, src, int(n.StartPoint().Row)+1))
				}
			}
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			visit(n.NamedChild(i))
		}
	}
	visit(body)
	return out
}

// callShape builds a Call from a call's function node. An identifier is a bare
// call. A selector is walked down to its base: the outermost field is the called
// name, the intermediate selector names are the field chain, and a base that is
// itself an identifier gives the qualifier (an alias or receiver). When the
// selector's operand is a call or any other non-identifier the qualifier is
// empty and the receiver is the unknown result of that expression.
func callShape(raw string, fn *sitter.Node, src []byte, line int) Call {
	c := Call{Raw: raw, Line: line}
	switch fn.Type() {
	case "identifier":
		c.Name = raw
	case "selector_expression":
		var fields []string // outer to inner
		base := fn
		for base.Type() == "selector_expression" {
			if f := base.ChildByFieldName("field"); f != nil {
				fields = append(fields, f.Content(src))
			}
			base = base.ChildByFieldName("operand")
			if base == nil {
				return c
			}
		}
		if len(fields) == 0 {
			return c
		}
		c.Field = fields[0]
		if base.Type() == "identifier" {
			c.Qualifier = base.Content(src)
		}
		for i := len(fields) - 1; i >= 1; i-- {
			c.Fields = append(c.Fields, fields[i])
		}
	}
	return c
}

// collectLocals adds every name declared anywhere in body to locals: :=, var,
// const, range and function-literal parameters (closure variables). A flat
// per-declaration scope is deliberate for the first graph slice; a nested variable's name
// is treated as local for the whole declaration, which can only turn an
// identifier call into deferred_local, never into a wrong stored edge.
func collectLocals(n *sitter.Node, src []byte, locals map[string]bool) {
	switch n.Type() {
	case "short_var_declaration":
		addAll(locals, identList(n.ChildByFieldName("left"), src))
	case "var_declaration", "const_declaration":
		addSpecNames(n, src, locals)
	case "range_clause":
		addAll(locals, identList(n.ChildByFieldName("left"), src))
	case "parameter_list":
		addAll(locals, paramNames(n, src))
	case "type_switch_statement":
		addAll(locals, identList(n.ChildByFieldName("alias"), src))
	case "receive_statement":
		addAll(locals, identList(n.ChildByFieldName("left"), src))
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		collectLocals(n.NamedChild(i), src, locals)
	}
}

// collectTypeNames records every named type (and type alias) declared by a
// type_declaration.
func collectTypeNames(decl *sitter.Node, src []byte, into map[string]string) {
	for i := 0; i < int(decl.NamedChildCount()); i++ {
		spec := decl.NamedChild(i)
		if spec.Type() == "type_spec" || spec.Type() == "type_alias" {
			if name := fieldName(spec, src); name != "" {
				into[name] = "type"
			}
		}
	}
}

// collectStructFields records the resolved type of every named field of each
// named struct type: grouped fields `a, b T` give both names T, and an embedded
// field (a field with no name) is skipped, so promotion is unseen (spec story
// 39, 60). A qualified type is resolved against the file's own aliases.
func collectStructFields(root *sitter.Node, src []byte, aliases map[string]string, into map[string]TypeRef) {
	for i := 0; i < int(root.NamedChildCount()); i++ {
		decl := root.NamedChild(i)
		if decl.Type() != "type_declaration" {
			continue
		}
		for j := 0; j < int(decl.NamedChildCount()); j++ {
			spec := decl.NamedChild(j)
			if spec.Type() != "type_spec" {
				continue
			}
			name := fieldName(spec, src)
			st := spec.ChildByFieldName("type")
			if name == "" || st == nil || st.Type() != "struct_type" {
				continue
			}
			collectFieldDecls(st, name, src, aliases, into)
		}
	}
}

// collectFieldDecls records each named field_declaration under a struct_type.
func collectFieldDecls(n *sitter.Node, structName string, src []byte, aliases map[string]string, into map[string]TypeRef) {
	if n.Type() == "field_declaration" {
		names := fieldNames(n, src)
		typ := n.ChildByFieldName("type")
		if len(names) > 0 && typ != nil {
			ref := typeRefFromWritten(typ.Content(src), aliases)
			for _, name := range names {
				into[structName+"\x00"+name] = ref
			}
		}
		return
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		collectFieldDecls(n.NamedChild(i), structName, src, aliases, into)
	}
}

// typeRefFromWritten reduces a written type to a resolved TypeRef for the
// declaring file: one leading `*` is stripped (a pointer and a value base
// resolve alike), `alias.Type` resolves through the file's own import aliases to
// an imported package path, and a bare identifier is a same-package type name.
// Any other form (slice, map, func, interface, generic instantiation, inline
// struct, or a qualified type through an unknown alias) is unknown (an empty
// TypeRef), never guessed.
func typeRefFromWritten(written string, aliases map[string]string) TypeRef {
	t := strings.TrimSpace(written)
	t = strings.TrimSpace(strings.TrimPrefix(t, "*"))
	if t == "" || strings.ContainsAny(t, "[](){} ,") {
		return TypeRef{}
	}
	if i := strings.LastIndexByte(t, '.'); i >= 0 {
		alias, name := t[:i], t[i+1:]
		path, ok := aliases[alias]
		if !ok || alias == "" || name == "" {
			return TypeRef{}
		}
		return TypeRef{Pkg: path, Name: name}
	}
	return TypeRef{Name: t}
}

// fieldNames returns the field_identifier names of a field_declaration, skipping
// the type node. An embedded field has only a type node and returns none.
func fieldNames(decl *sitter.Node, src []byte) []string {
	typ := decl.ChildByFieldName("type")
	var out []string
	for i := 0; i < int(decl.NamedChildCount()); i++ {
		c := decl.NamedChild(i)
		if typ != nil && c.StartByte() == typ.StartByte() && c.EndByte() == typ.EndByte() {
			continue
		}
		if c.Type() == "field_identifier" {
			out = append(out, c.Content(src))
		}
	}
	return out
}

// collectFuncResults records each package-level function's single declared
// result resolved type, so a same-package constructor call can be typed (spec
// story 39). A function with zero or two-or-more results, or a method, is not a
// constructor and is not recorded.
func collectFuncResults(root *sitter.Node, src []byte, aliases map[string]string, into map[string]TypeRef) {
	for i := 0; i < int(root.NamedChildCount()); i++ {
		n := root.NamedChild(i)
		if n.Type() != "function_declaration" {
			continue
		}
		name := fieldName(n, src)
		if name == "" {
			continue
		}
		if rt, ok := singleResult(n, src); ok {
			into[name] = typeRefFromWritten(rt, aliases)
		}
	}
}

// singleResult returns the one declared result type of a function declaration. A
// single result written directly is that type; a parenthesised result list is a
// single result only when it declares exactly one result. Results are counted by
// NAME, so a grouped named result `(a, b T)` is two results, not one.
func singleResult(n *sitter.Node, src []byte) (string, bool) {
	r := n.ChildByFieldName("result")
	if r == nil {
		return "", false
	}
	if r.Type() != "parameter_list" {
		return r.Content(src), true
	}
	var types []string
	total := 0
	for i := 0; i < int(r.NamedChildCount()); i++ {
		p := r.NamedChild(i)
		if p.Type() != "parameter_declaration" {
			continue
		}
		if names := nameIdentifiers(p, src); len(names) > 0 {
			total += len(names)
		} else {
			total++
		}
		if t := p.ChildByFieldName("type"); t != nil {
			types = append(types, t.Content(src))
		}
	}
	if total != 1 || len(types) != 1 {
		return "", false
	}
	return types[0], true
}

// collectSpecNames records the names of a var_declaration or const_declaration
// as package-level declared names of the given kind.
func collectSpecNames(decl *sitter.Node, src []byte, kind string, into map[string]string) {
	for _, name := range specNames(decl, src) {
		into[name] = kind
	}
}

// addSpecNames records the names declared by a var or const declaration as
// locals.
func addSpecNames(decl *sitter.Node, src []byte, locals map[string]bool) {
	addAll(locals, specNames(decl, src))
}

// specNames returns the names declared by a var_declaration or
// const_declaration, descending into a grouped var_spec_list/const_spec_list.
func specNames(decl *sitter.Node, src []byte) []string {
	var out []string
	specNameList(decl, src, &out)
	return out
}

// specNameList appends the names of every var_spec/const_spec under n,
// including a grouped var_spec_list or const_spec_list.
func specNameList(n *sitter.Node, src []byte, out *[]string) {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		spec := n.NamedChild(i)
		switch spec.Type() {
		case "var_spec", "const_spec":
			*out = append(*out, nameIdentifiers(spec, src)...)
		case "var_spec_list", "const_spec_list":
			specNameList(spec, src, out)
		}
	}
}

// paramNames returns the parameter names of a parameter_list, skipping the type
// names (so a parameter's type is never mistaken for a local).
func paramNames(params *sitter.Node, src []byte) []string {
	if params == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(params.NamedChildCount()); i++ {
		p := params.NamedChild(i)
		if p.Type() == "parameter_declaration" || p.Type() == "variadic_parameter_declaration" {
			out = append(out, nameIdentifiers(p, src)...)
		}
	}
	return out
}

// typeParamNames returns the type parameter names of a type_parameter_list,
// skipping the constraint types (so a constraint name is never mistaken for a
// type parameter).
func typeParamNames(params *sitter.Node, src []byte) []string {
	if params == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(params.NamedChildCount()); i++ {
		p := params.NamedChild(i)
		if p.Type() == "type_parameter_declaration" {
			out = append(out, nameIdentifiers(p, src)...)
		}
	}
	return out
}

// nameIdentifiers returns every leading identifier name of a parameter, var or
// const spec. tree-sitter gives each name in `a, b T` its own identifier node
// with the same "name" field (so ChildByFieldName returns only the first), and a
// bare type may itself be an identifier, so the type node is excluded by byte
// range.
func nameIdentifiers(spec *sitter.Node, src []byte) []string {
	typ := spec.ChildByFieldName("type")
	var out []string
	for i := 0; i < int(spec.NamedChildCount()); i++ {
		c := spec.NamedChild(i)
		if typ != nil && c.StartByte() == typ.StartByte() && c.EndByte() == typ.EndByte() {
			continue
		}
		if c.Type() == "identifier" {
			out = append(out, c.Content(src))
		}
	}
	return out
}

// identList returns the leaf identifier texts under n, or nil for nil.
func identList(n *sitter.Node, src []byte) []string {
	if n == nil {
		return nil
	}
	switch n.Type() {
	case "identifier", "type_identifier", "field_identifier", "package_identifier":
		return []string{n.Content(src)}
	}
	var out []string
	for i := 0; i < int(n.NamedChildCount()); i++ {
		out = append(out, identList(n.NamedChild(i), src)...)
	}
	return out
}

// addAll adds names to a set.
func addAll(set map[string]bool, names []string) {
	for _, n := range names {
		if n != "" {
			set[n] = true
		}
	}
}

// fieldName returns the source text of node's "name" field, or "".
func fieldName(n *sitter.Node, src []byte) string {
	f := n.ChildByFieldName("name")
	if f == nil {
		return ""
	}
	return f.Content(src)
}

// receiverText returns the receiver type as written (for example *Scheduler),
// preserving pointer versus value.
func receiverText(recv *sitter.Node, src []byte) string {
	decl := recv.NamedChild(0)
	if decl == nil {
		return ""
	}
	typ := decl.ChildByFieldName("type")
	if typ == nil {
		return ""
	}
	return typ.Content(src)
}
