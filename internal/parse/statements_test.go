package parse_test

import (
	"reflect"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
)

// TestBodyStatements pins the top-level statement ranges of a declaration body:
// nested block statements are not reported, a multi-line statement is one
// range, and a declaration without a body is absent from the map.
func TestBodyStatements(t *testing.T) {
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
	got := parse.BodyStatements("pkg/p/statements.go", src)
	want := map[int][]parse.LineRange{
		4:  {{StartLine: 5, EndLine: 5}, {StartLine: 6, EndLine: 6}},
		9:  {{StartLine: 10, EndLine: 12}, {StartLine: 13, EndLine: 16}},
		23: {{StartLine: 24, EndLine: 24}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BodyStatements = %#v, want %#v", got, want)
	}
	if _, ok := got[19]; ok {
		t.Errorf("body-less declaration at line 19 was reported")
	}
}
