package parse

import (
	"context"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
)

// LineRange is an inclusive 1-based line span.
type LineRange struct {
	StartLine int
	EndLine   int
}

// Facts is the per-file parse result the chunker reads, from one tree-sitter
// pass: the top-level statement ranges of each function and method body (for
// splitting an over-ceiling function), and the single declared result type of
// each function (for recognising a same-file constructor by return type). Both
// maps are keyed by a declaration's start line. Statements has no entry for a
// declaration without a body; Results maps a function to "" when it has zero or
// more than one declared result.
type Facts struct {
	Statements map[int][]LineRange
	Results    map[int]string
}

// FileFacts parses src as the Go file at repo-root-relative relPath and returns
// the chunker [Facts] in a single tree-sitter pass. It never returns nil maps. A
// top-level function contributes both its statement ranges and its single
// declared result type; a method contributes only its statement ranges. Nested
// blocks are not descended into, so an if/for/switch is one range and comments
// between statements are not statements.
func FileFacts(relPath string, src []byte) Facts {
	facts := Facts{
		Statements: map[int][]LineRange{},
		Results:    map[int]string{},
	}

	p := sitter.NewParser()
	defer p.Close()
	p.SetLanguage(golang.GetLanguage())

	tree, err := p.ParseCtx(context.Background(), nil, src)
	if err != nil {
		return facts
	}
	defer tree.Close()
	root := tree.RootNode()

	for i := 0; i < int(root.NamedChildCount()); i++ {
		n := root.NamedChild(i)
		switch n.Type() {
		case "function_declaration":
			start := int(n.StartPoint().Row) + 1
			rt, ok := singleResult(n, src)
			if !ok {
				rt = ""
			}
			facts.Results[start] = rt
			if body := n.ChildByFieldName("body"); body != nil {
				facts.Statements[start] = bodyStatementRanges(body)
			}
		case "method_declaration":
			if body := n.ChildByFieldName("body"); body != nil {
				facts.Statements[int(n.StartPoint().Row)+1] = bodyStatementRanges(body)
			}
		}
	}
	return facts
}

// bodyStatementRanges returns the line ranges of the direct top-level statements
// of a body, skipping comments.
func bodyStatementRanges(body *sitter.Node) []LineRange {
	ranges := make([]LineRange, 0, body.NamedChildCount())
	for j := 0; j < int(body.NamedChildCount()); j++ {
		c := body.NamedChild(j)
		if c.Type() == "comment" {
			continue
		}
		ranges = append(ranges, LineRange{
			StartLine: int(c.StartPoint().Row) + 1,
			EndLine:   int(c.EndPoint().Row) + 1,
		})
	}
	return ranges
}

// singleResult returns the one declared result type of a function declaration.
// A result written directly is that type; a parenthesised result list is a
// single result only when it declares exactly one result, counted by name so a
// grouped `(a, b T)` is two results. The name of a single named result is
// dropped, so `(p *Pod)` yields "*Pod".
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
		names := 0
		for j := 0; j < int(p.NamedChildCount()); j++ {
			if p.NamedChild(j).Type() == "identifier" {
				names++
			}
		}
		if names > 0 {
			total += names
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
