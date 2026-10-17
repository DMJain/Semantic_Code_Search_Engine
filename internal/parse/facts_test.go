package parse_test

import (
	"reflect"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
)

// TestFileFactsStatements pins the top-level statement ranges of a declaration
// body: nested block statements are not reported, a multi-line statement is one
// range, and a declaration without a body is absent from the map.
func TestFileFactsStatements(t *testing.T) {
	src := []byte(`package p

// Doc
func Simple() {
	x := 1
	y := 2
}

func Multi() {
	if x {
		y()
	}
	z := []int{
		1,
		2,
	}
}

func NoBody()

type T int

func (t T) Method() {
	return
}
`)
	got := parse.FileFacts(src).Statements
	want := map[int][]parse.LineRange{
		4:  {{StartLine: 5, EndLine: 5}, {StartLine: 6, EndLine: 6}},
		9:  {{StartLine: 10, EndLine: 12}, {StartLine: 13, EndLine: 16}},
		23: {{StartLine: 24, EndLine: 24}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Statements = %#v, want %#v", got, want)
	}
	if _, ok := got[19]; ok {
		t.Errorf("body-less declaration at line 19 was reported")
	}
}

// TestFileFactsResults pins the single declared result type of each top-level
// function by its start line: the written type text, or "" for zero results, a
// parenthesised multi-result list, or a grouped named result pair. A single
// named result keeps only its type. Methods and type declarations are absent.
func TestFileFactsResults(t *testing.T) {
	src := []byte(`package p

func NoResult() {}

func One() int { return 0 }

func Ptr() *Pod { return nil }

func Named() (p *Pod) { return nil }

func Two() (int, error) { return 0, nil }

func Grouped() (a, b int) { return 0, 0 }

func MultiUnnamed() (int, int) { return 0, 0 }

func Slice() []int { return nil }

type Pod struct{}

func (p Pod) Method() int { return 0 }
`)
	got := parse.FileFacts(src).Results
	want := map[int]string{
		3:  "",
		5:  "int",
		7:  "*Pod",
		9:  "*Pod",
		11: "",
		13: "",
		15: "",
		17: "[]int",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Results = %#v, want %#v", got, want)
	}
}

// TestFileFactsNoBody proves a body-less declaration still reports its result
// and a syntax-broken file does not panic, while returning non-nil maps.
func TestFileFactsNoBody(t *testing.T) {
	got := parse.FileFacts([]byte("package p\n\nfunc Ext() error\n"))
	if got.Results[3] != "error" {
		t.Errorf("body-less result = %q, want %q", got.Results[3], "error")
	}
	if got.Statements == nil || got.Results == nil {
		t.Errorf("FileFacts returned a nil map: %+v", got)
	}
	_ = parse.FileFacts([]byte("package p\n\nfunc Bad( {\n"))
}

// TestTopLevelDecls pins the ranges of top-level const, var and type
// declarations, including grouped blocks and struct/interface/alias types, in
// source order; functions and methods are not declarations.
func TestTopLevelDecls(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []parse.LineRange
	}{
		{"const-only", "package p\n\nconst c = 1\n", []parse.LineRange{{StartLine: 3, EndLine: 3}}},
		{"var-only", "package p\n\nvar v = 2\n", []parse.LineRange{{StartLine: 3, EndLine: 3}}},
		{"type-only", "package p\n\ntype T = int\n", []parse.LineRange{{StartLine: 3, EndLine: 3}}},
		{"none", "package p\n\n// just a comment\n", nil},
		{"mixed", `package p

const c = 1

var v = 2

type Alias = int

func F() {}

type S struct{}

const (
	grouped = 3
)

var (
	groupedVar = 4
)
`, []parse.LineRange{
			{StartLine: 3, EndLine: 3},
			{StartLine: 5, EndLine: 5},
			{StartLine: 7, EndLine: 7},
			{StartLine: 11, EndLine: 11},
			{StartLine: 13, EndLine: 15},
			{StartLine: 17, EndLine: 19},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parse.TopLevelDecls([]byte(tc.src)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("TopLevelDecls = %#v, want %#v", got, tc.want)
			}
		})
	}
}
