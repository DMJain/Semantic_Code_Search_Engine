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

// BodyStatements returns, for each top-level function/method declaration in
// src, the line ranges of the direct top-level statements of its body, keyed by
// the declaration's start line. A declaration without a body is absent. Nested
// blocks are not descended into, so an if/for/switch is one range; comments
// between statements are not statements.
func BodyStatements(relPath string, src []byte) map[int][]LineRange {
	p := sitter.NewParser()
	defer p.Close()
	p.SetLanguage(golang.GetLanguage())

	tree, err := p.ParseCtx(context.Background(), nil, src)
	if err != nil {
		return map[int][]LineRange{}
	}
	defer tree.Close()
	root := tree.RootNode()

	out := make(map[int][]LineRange)
	for i := 0; i < int(root.NamedChildCount()); i++ {
		n := root.NamedChild(i)
		if n.Type() != "function_declaration" && n.Type() != "method_declaration" {
			continue
		}
		body := n.ChildByFieldName("body")
		if body == nil {
			continue
		}
		start := int(n.StartPoint().Row) + 1
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
		out[start] = ranges
	}
	return out
}
