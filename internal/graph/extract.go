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
	// Types maps the receiver name and every parameter name to its written type
	// text (for example `*Scheduler`, `Interface`, `p *pkg.T`, `f func()`). It is
	// the per-function type environment Rule 3 resolves against. A name
	// redeclared anywhere in the body is removed, so a shadowed name is unknown
	// (an inner variable never inherits the outer type). Unknown forms are kept
	// as written and rejected by the resolver, which never guesses.
	Types map[string]string
	// TypeParams is the set of the declaration's own type parameter names. A
	// receiver or parameter typed as one of them is unknown, so a type parameter
	// that shares a name with a package type is never resolved to that type.
	TypeParams map[string]bool
	Calls      []Call
}

// Call is one call expression. Name is the identifier for a bare identifier
// call. Qualifier and Field are set for a selector call whose operand is a bare
// identifier (the alias-qualified `alias.Name` shape). Raw is set for every
// call; a call with neither Name nor Qualifier is a receiver-style selector or a
// chained call and stays a deferred selector.
type Call struct {
	Raw       string
	Name      string
	Qualifier string
	Field     string
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
// stories 38, 101). Each declaration carries the receiver and parameter types as
// written (the Rule 3 type environment); locals, fields and constructor return
// types are not resolved here (later tickets).
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

// DeclaredNames re-parses src and returns only its package-level declared names
// by kind. It is the names-only path used to build the cross-package
// declared-name index (spec story 101) without extracting call sites.
func DeclaredNames(src []byte) (map[string]string, error) {
	tree, err := parseTree(src)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	out := map[string]string{}
	collectDeclaredNames(tree.RootNode(), src, out)
	return out, nil
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
		Name:       fieldName(n, src),
		StartLine:  int(n.StartPoint().Row) + 1,
		Locals:     map[string]bool{},
		Types:      map[string]string{},
		TypeParams: map[string]bool{},
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
		removeShadowed(body, src, d.Types)
		d.Calls = collectCalls(body, src)
	}
	return d
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
					c := Call{Raw: raw, Line: int(n.StartPoint().Row) + 1}
					switch fn.Type() {
					case "identifier":
						c.Name = raw
					case "selector_expression":
						operand := fn.ChildByFieldName("operand")
						field := fn.ChildByFieldName("field")
						if operand != nil && field != nil && operand.Type() == "identifier" {
							c.Qualifier = operand.Content(src)
							c.Field = field.Content(src)
						}
					}
					out = append(out, c)
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
