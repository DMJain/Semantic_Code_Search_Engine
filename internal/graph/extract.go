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
	Calls  []Call
}

// Call is one call expression. Name is the identifier for a bare identifier
// call, and empty for any other operand (a selector or a chained call), which is
// a deferred selector.
type Call struct {
	Raw  string
	Name string
	Line int
}

// FileInfo is the extraction result for one file: its declared functions and
// methods, and its package-level declared names. Declared maps a name to its
// kind ("type", "func", "var" or "const") so the resolver can tell a type
// conversion from a call through a package-level function variable.
type FileInfo struct {
	Decls    []Decl
	Declared map[string]string
}

// Extract re-parses src and returns its declared functions/methods with their
// call sites and its package-level declared names (spec story 101). Import
// aliases and receiver/parameter types are not resolved here (this slice needs
// only identifier calls and local scope).
func Extract(src []byte) (FileInfo, error) {
	p := sitter.NewParser()
	defer p.Close()
	p.SetLanguage(golang.GetLanguage())

	tree, err := p.ParseCtx(context.Background(), nil, src)
	if err != nil {
		return FileInfo{}, err
	}
	defer tree.Close()
	root := tree.RootNode()

	info := FileInfo{Declared: map[string]string{}}
	for i := 0; i < int(root.NamedChildCount()); i++ {
		n := root.NamedChild(i)
		switch n.Type() {
		case "function_declaration":
			if name := fieldName(n, src); name != "" {
				info.Declared[name] = "func"
			}
			info.Decls = append(info.Decls, extractDecl(n, src))
		case "method_declaration":
			info.Decls = append(info.Decls, extractDecl(n, src))
		case "type_declaration":
			collectTypeNames(n, src, info.Declared)
		case "var_declaration":
			collectSpecNames(n, src, "var", info.Declared)
		case "const_declaration":
			collectSpecNames(n, src, "const", info.Declared)
		}
	}
	return info, nil
}

// extractDecl builds one Decl from a function_declaration or method_declaration.
func extractDecl(n *sitter.Node, src []byte) Decl {
	d := Decl{
		Name:      fieldName(n, src),
		StartLine: int(n.StartPoint().Row) + 1,
		Locals:    map[string]bool{},
	}
	if recv := n.ChildByFieldName("receiver"); recv != nil {
		d.Receiver = receiverText(recv, src)
		if decl := recv.NamedChild(0); decl != nil {
			addAll(d.Locals, nameIdentifiers(decl, src))
		}
	}
	addAll(d.Locals, paramNames(n.ChildByFieldName("parameters"), src))
	if body := n.ChildByFieldName("body"); body != nil {
		collectLocals(body, src, d.Locals)
		d.Calls = collectCalls(body, src)
	}
	return d
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
					if fn.Type() == "identifier" {
						c.Name = raw
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
