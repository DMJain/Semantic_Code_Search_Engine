package parse

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/walk"
)

// Symbol kinds. Only func and method are produced here; struct and interface
// (S1-T05) and raw_calls (S1-T06) extend this without changing the shape.
const (
	// KindFunc is a top-level function declaration.
	KindFunc = "func"
	// KindMethod is a declaration with a receiver.
	KindMethod = "method"
)

// ErrSyntax reports that tree-sitter found ERROR nodes in a file. [File] still
// returns the symbols it managed to extract; the caller counts the file as a
// parse error and stores it anyway.
var ErrSyntax = errors.New("parse: syntax errors")

// Symbol is one function or method extracted from a Go file. QualifiedName is
// the base name (<import path>.<Name>, or <import path>.(*T).Name for
// methods); [AssignQualifiedNames] appends #n suffixes to duplicates across a
// package.
type Symbol struct {
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Receiver      string `json:"receiver,omitempty"`
	Signature     string `json:"signature"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
	Doc           string `json:"doc,omitempty"`
}

// File parses src as the Go file at repo-root-relative slash path relPath and
// returns its function and method symbols in source order. The base qualified
// name uses walk.ImportPath(relPath). If tree-sitter finds ERROR nodes it
// returns the symbols it extracted together with [ErrSyntax].
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
	}
	if kind == KindMethod {
		sym.Receiver = receiverType(n, src)
		sym.QualifiedName = fmt.Sprintf("%s.(%s).%s", pkg, sym.Receiver, name)
	} else {
		sym.QualifiedName = pkg + "." + name
	}
	return sym
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
