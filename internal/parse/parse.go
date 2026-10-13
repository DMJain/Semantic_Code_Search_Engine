package parse

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/walk"
)

// Symbol kinds. Func, method, struct and interface are produced here;
// raw_calls extends the shape without adding a kind.
const (
	// KindFunc is a top-level function declaration.
	KindFunc = "func"
	// KindMethod is a declaration with a receiver.
	KindMethod = "method"
	// KindStruct is a named struct type declaration.
	KindStruct = "struct"
	// KindInterface is a named interface type declaration.
	KindInterface = "interface"
)

// maxSignature is the header length cap. A type's signature is the declaration
// header only (never a field or method body), so the cap only bites on a type
// with an unusually long type-parameter list.
const maxSignature = 200

const (
	// maxRawCall is the per-call length cap: the callee text is truncated to
	// this many runes (not bytes), so a multi-byte identifier is never split.
	maxRawCall = 80
	// maxRawCalls is the per-symbol size cap, applied after dedup in source
	// order.
	maxRawCalls = 40
)

// builtins is the fixed set of predeclared identifiers dropped from raw calls:
// the builtin functions (len, append, make, ...) and the predeclared type names
// used as conversions (int, string, any, ...). A call is dropped when the whole
// function-operand text is exactly one of these bare names. A conversion to a
// user-defined type, for example MyType(x), is kept because without type
// information it is syntactically identical to a function call. error is
// deliberately absent (it cannot be used as a conversion). It is the single
// source of truth for the raw-call drop rule.
var builtins = map[string]bool{
	// Builtin functions.
	"len": true, "cap": true, "make": true, "new": true, "append": true,
	"copy": true, "delete": true, "real": true, "imag": true, "close": true,
	"panic": true, "recover": true, "print": true, "println": true,
	"min": true, "max": true, "clear": true, "complex": true,
	// Predeclared types (conversions).
	"string": true, "bool": true, "byte": true, "rune": true, "any": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "float32": true, "float64": true,
	"complex64": true, "complex128": true,
}

// ErrSyntax reports that tree-sitter found ERROR nodes in a file. [File] still
// returns the symbols it managed to extract; the caller counts the file as a
// parse error and stores it anyway.
var ErrSyntax = errors.New("parse: syntax errors")

// IsBuiltin reports whether name is one of the predeclared identifiers: the
// builtin functions (len, append, ...) and the predeclared type names used as
// conversions (string, int, ...). It exposes the single [builtins] list so the
// graph stage uses the same set as raw-call extraction (D-073).
func IsBuiltin(name string) bool { return builtins[name] }

// Symbol is one function, method, struct or interface extracted from a Go
// file. QualifiedName is the base name (<import path>.<Name>, or
// <import path>.(*T).Name for methods); [AssignQualifiedNames] appends #n
// suffixes to duplicates across a package.
type Symbol struct {
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Receiver      string `json:"receiver,omitempty"`
	Signature     string `json:"signature"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
	Doc           string `json:"doc,omitempty"`
	// RawCalls is the deduplicated, source-ordered list of callee texts for a
	// function or method, capped at [maxRawCalls] entries of at most
	// [maxRawCall] runes each. It is summary-prompt input only and never feeds
	// the call graph (D-019). It is empty for struct and interface symbols.
	RawCalls []string `json:"raw_calls,omitempty"`
}

// File parses src as the Go file at repo-root-relative slash path relPath and
// returns its function, method, struct and interface symbols in source order.
// The base qualified name uses walk.ImportPath(relPath). If tree-sitter finds
// ERROR nodes it returns the symbols it extracted together with [ErrSyntax].
func File(relPath string, src []byte) ([]Symbol, error) {
	p := sitter.NewParser()
	defer p.Close()
	p.SetLanguage(golang.GetLanguage())

	tree, err := p.ParseCtx(context.Background(), nil, src)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	root := tree.RootNode()

	pkg := walk.ImportPath(relPath)
	var syms []Symbol
	for i := 0; i < int(root.NamedChildCount()); i++ {
		n := root.NamedChild(i)
		switch n.Type() {
		case "function_declaration":
			syms = append(syms, symbol(n, src, pkg, KindFunc))
		case "method_declaration":
			syms = append(syms, symbol(n, src, pkg, KindMethod))
		case "type_declaration":
			syms = append(syms, typeSymbols(n, src, pkg)...)
		}
	}
	if root.HasError() {
		return syms, ErrSyntax
	}
	return syms, nil
}

// symbol builds one Symbol from a function_declaration or method_declaration
// node.
func symbol(n *sitter.Node, src []byte, pkg, kind string) Symbol {
	name := n.ChildByFieldName("name").Content(src)
	sym := Symbol{
		Kind:      kind,
		Name:      name,
		Signature: signature(n, src),
		StartLine: int(n.StartPoint().Row) + 1,
		EndLine:   int(n.EndPoint().Row) + 1,
		Doc:       doc(src, int(n.StartPoint().Row)),
		RawCalls:  rawCalls(n.ChildByFieldName("body"), src),
	}
	if kind == KindMethod {
		sym.Receiver = receiverType(n, src)
		sym.QualifiedName = fmt.Sprintf("%s.(%s).%s", pkg, sym.Receiver, name)
	} else {
		sym.QualifiedName = pkg + "." + name
	}
	return sym
}

// typeSymbols builds one Symbol per named struct or interface in a
// type_declaration, in source order. A grouped `type ( ... )` block yields one
// symbol per spec; `type X = Y` aliases and other named types (for example
// `type X int`) are skipped. Each symbol carries the spec's own span, so a
// grouped symbol starts at its spec line, not at the `type (` keyword.
func typeSymbols(n *sitter.Node, src []byte, pkg string) []Symbol {
	var syms []Symbol
	for i := 0; i < int(n.NamedChildCount()); i++ {
		spec := n.NamedChild(i)
		if spec.Type() != "type_spec" {
			continue
		}
		kind := ""
		switch spec.ChildByFieldName("type").Type() {
		case "struct_type":
			kind = KindStruct
		case "interface_type":
			kind = KindInterface
		default:
			continue
		}
		name := spec.ChildByFieldName("name").Content(src)
		syms = append(syms, Symbol{
			Kind:          kind,
			Name:          name,
			QualifiedName: pkg + "." + name,
			Signature:     typeSignature(spec, src, kind),
			StartLine:     int(spec.StartPoint().Row) + 1,
			EndLine:       int(spec.EndPoint().Row) + 1,
			Doc:           doc(src, int(spec.StartPoint().Row)),
		})
	}
	return syms
}

// typeSignature is the header only: `type Name`, any type-parameter list, then
// the kind (`struct` or `interface`). Field and method bodies are never
// inlined, for example `type Foo[T any] struct` or `type Reader interface`. The
// result is capped at [maxSignature] characters.
func typeSignature(spec *sitter.Node, src []byte, kind string) string {
	sig := "type " + spec.ChildByFieldName("name").Content(src)
	if tp := spec.ChildByFieldName("type_parameters"); tp != nil {
		sig += tp.Content(src)
	}
	sig += " " + kind
	if len(sig) > maxSignature {
		if r := []rune(sig); len(r) > maxSignature {
			sig = string(r[:maxSignature])
		}
	}
	return sig
}

// signature is the declaration header from the func keyword through the result
// list, with the body removed. A body-less declaration uses the whole node.
func signature(n *sitter.Node, src []byte) string {
	end := n.EndByte()
	if body := n.ChildByFieldName("body"); body != nil {
		end = body.StartByte()
	}
	return strings.TrimSpace(string(src[n.StartByte():end]))
}

// receiverType returns the receiver type as written (for example *Scheduler or
// Scheduler), preserving pointer versus value. It is empty for a
// function_declaration.
func receiverType(n *sitter.Node, src []byte) string {
	recv := n.ChildByFieldName("receiver")
	if recv == nil {
		return ""
	}
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

// rawCalls walks a function or method body (including any nested func_literal)
// in pre-order and collects each call_expression's function-operand source text:
// the callee as written, for example `sched.cache.AssumePod` or `klog.V(2).InfoS`.
// For a chain the outer call is seen before its operands, so `a.B().C()` yields
// `a.B().C` then `a.B` (the inner call is itself a call_expression). The text is
// trimmed, truncated to [maxRawCall] runes, dropped if it is a bare [builtins]
// name, deduplicated keeping the first occurrence, and capped at [maxRawCalls].
// A declaration without a body (for example an external func) yields nil.
func rawCalls(body *sitter.Node, src []byte) []string {
	if body == nil {
		return nil
	}
	var out []string
	seen := make(map[string]bool)
	var visit func(n *sitter.Node)
	visit = func(n *sitter.Node) {
		if n.Type() == "call_expression" {
			fn := n.ChildByFieldName("function")
			if fn != nil {
				text := truncateRunes(strings.TrimSpace(fn.Content(src)), maxRawCall)
				if text != "" && !builtins[text] && !seen[text] && len(out) < maxRawCalls {
					seen[text] = true
					out = append(out, text)
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

// truncateRunes cuts s to at most limit runes, never splitting a multi-byte rune.
func truncateRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit])
}

// doc returns the contiguous // comment lines immediately above the
// declaration whose first line is startRow (0-based). A blank line or any line
// that is not a // comment ends the run.
func doc(src []byte, startRow int) string {
	lines := strings.Split(string(src), "\n")
	i := startRow - 1
	for i >= 0 && strings.HasPrefix(strings.TrimSpace(lines[i]), "//") {
		i--
	}
	return strings.Join(lines[i+1:startRow], "\n")
}

// FileSymbols pairs a repo-root-relative slash path with the symbols parsed
// from it, in source order.
type FileSymbols struct {
	Path    string
	Symbols []Symbol
}

// AssignQualifiedNames appends a deterministic #n suffix to duplicate base
// qualified names across the files of one package. Duplicates are ordered by
// (path, start_line); the first occurrence keeps the base name and each later
// one gets #1, #2, ... The files slice is modified in place, so callers must
// pass the same Symbol values they will store.
func AssignQualifiedNames(files []FileSymbols) {
	type occurrence struct {
		file int
		sym  int
	}
	byName := make(map[string][]occurrence)
	for fi := range files {
		for si := range files[fi].Symbols {
			q := files[fi].Symbols[si].QualifiedName
			byName[q] = append(byName[q], occurrence{fi, si})
		}
	}
	for _, occs := range byName {
		if len(occs) < 2 {
			continue
		}
		slices.SortFunc(occs, func(a, b occurrence) int {
			if c := strings.Compare(files[a.file].Path, files[b.file].Path); c != 0 {
				return c
			}
			return files[a.file].Symbols[a.sym].StartLine - files[b.file].Symbols[b.sym].StartLine
		})
		for i := 1; i < len(occs); i++ {
			o := occs[i]
			base := files[o.file].Symbols[o.sym].QualifiedName
			files[o.file].Symbols[o.sym].QualifiedName = fmt.Sprintf("%s#%d", base, i)
		}
	}
}
