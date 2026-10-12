package parse_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
)

// update regenerates the golden file. It is manual only: `go test` and CI never
// pass -update, so a golden is never rewritten by a normal test run.
var update = flag.Bool("update", false, "regenerate golden files (manual only)")

// TestFileGolden pins the full Symbol JSON for testdata/sample.go, which covers
// pointer and value receivers, generics, multiple returns, a variadic
// parameter, an embedded-type method and doc comments with a blank-line gap.
func TestFileGolden(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "sample.go"))
	if err != nil {
		t.Fatal(err)
	}
	syms, err := parse.File("pkg/scheduler/sample.go", src)
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	got, err := json.MarshalIndent(syms, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	golden := filepath.Join("testdata", "sample.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("golden mismatch for sample.go (regenerate with -update if intended):\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestFileTypesGolden pins the Symbol JSON for testdata/types.go, which covers
// a lone struct, a lone interface, a generic struct, an embedded interface, a
// grouped type block (one symbol per spec) and a `type X = Y` alias plus a
// named non-struct type, neither of which is a symbol.
func TestFileTypesGolden(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "types.go"))
	if err != nil {
		t.Fatal(err)
	}
	syms, err := parse.File("pkg/scheduler/types.go", src)
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	got, err := json.MarshalIndent(syms, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	golden := filepath.Join("testdata", "types.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("golden mismatch for types.go (regenerate with -update if intended):\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestFileRawCallsGolden pins the raw_calls list for testdata/calls.go: method
// chains (outer call before its inner operand), builtins and a predeclared-type
// conversion dropped, a call inside a nested func_literal included, and a
// repeated call deduplicated. Source order is preserved.
func TestFileRawCallsGolden(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "calls.go"))
	if err != nil {
		t.Fatal(err)
	}
	syms, err := parse.File("pkg/scheduler/calls.go", src)
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	got, err := json.MarshalIndent(syms, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	golden := filepath.Join("testdata", "calls.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("golden mismatch for calls.go (regenerate with -update if intended):\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestRawCallCaps proves the per-symbol cap is 40 and the per-call cap is 80
// runes, never splitting a multi-byte rune.
func TestRawCallCaps(t *testing.T) {
	var many strings.Builder
	many.WriteString("package p\n\nfunc F() {\n")
	for i := 0; i < 45; i++ {
		fmt.Fprintf(&many, "\tf%d()\n", i)
	}
	many.WriteString("}\n")
	syms, err := parse.File("pkg/p/cap.go", []byte(many.String()))
	if err != nil {
		t.Fatal(err)
	}
	got := syms[0].RawCalls
	if len(got) != 40 {
		t.Fatalf("raw call count = %d, want 40", len(got))
	}
	if got[0] != "f0" || got[39] != "f39" {
		t.Errorf("capped list = first %q last %q, want first f0 last f39", got[0], got[39])
	}

	long := "x" + strings.Repeat("a", 90)
	ascii, err := parse.File("pkg/p/long.go", []byte("package p\n\nfunc G() {\n\t"+long+"()\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c := ascii[0].RawCalls; len(c) != 1 || c[0] != long[:80] {
		t.Errorf("ascii truncation = %q, want %q", c, long[:80])
	}

	uni := "x" + strings.Repeat("é", 90)
	multi, err := parse.File("pkg/p/uni.go", []byte("package p\n\nfunc H() {\n\t"+uni+"()\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c := multi[0].RawCalls; len(c) != 1 {
		t.Fatalf("multi-byte raw calls = %v, want one", c)
	} else if c[0] != string([]rune(uni)[:80]) || len([]rune(c[0])) != 80 {
		t.Errorf("multi-byte truncation = %q (%d runes), want 80 runes", c[0], len([]rune(c[0])))
	}
}

// TestTypeSignatureHeaderOnly proves a type's signature is its header only:
// bounded by 200 characters and free of field or method bodies.
func TestTypeSignatureHeaderOnly(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "types.go"))
	if err != nil {
		t.Fatal(err)
	}
	syms, err := parse.File("pkg/scheduler/types.go", src)
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	byName := make(map[string]parse.Symbol, len(syms))
	for _, s := range syms {
		byName[s.Name] = s
	}

	if aliases := byName["Alias"]; aliases.Kind != "" {
		t.Errorf("Alias is an alias and must not be a symbol: %+v", aliases)
	}
	if myInt := byName["MyInt"]; myInt.Kind != "" {
		t.Errorf("MyInt is a named non-struct type and must not be a symbol: %+v", myInt)
	}

	cases := []struct {
		name string
		kind string
		sig  string
	}{
		{"Lone", parse.KindStruct, "type Lone struct"},
		{"Plain", parse.KindInterface, "type Plain interface"},
		{"Set", parse.KindStruct, "type Set[T comparable] struct"},
		{"ReadWriter", parse.KindInterface, "type ReadWriter interface"},
		{"GroupedStruct", parse.KindStruct, "type GroupedStruct struct"},
		{"GroupedInterface", parse.KindInterface, "type GroupedInterface interface"},
	}
	for _, c := range cases {
		s := byName[c.name]
		if s.Kind != c.kind {
			t.Errorf("%s kind = %q, want %q", c.name, s.Kind, c.kind)
		}
		if s.Signature != c.sig {
			t.Errorf("%s signature = %q, want %q", c.name, s.Signature, c.sig)
		}
		if len(s.Signature) > 200 {
			t.Errorf("%s signature length = %d, want <= 200", c.name, len(s.Signature))
		}
	}

	for _, body := range []string{"FieldA", "FieldB", "members", "Do(", "M(", "{", "}"} {
		for name, s := range byName {
			if s.Kind != parse.KindStruct && s.Kind != parse.KindInterface {
				continue
			}
			if strings.Contains(s.Signature, body) {
				t.Errorf("%s header-only signature %q contains body token %q", name, s.Signature, body)
			}
		}
	}
}

// TestTypeSignatureCap proves an over-long type-parameter list is truncated to
// 200 characters rather than inlined.
func TestTypeSignatureCap(t *testing.T) {
	src := []byte("package p\n\ntype Big[" + strings.Repeat("T any, ", 40) + "Last any] struct {\n\tHidden int\n}\n")
	syms, err := parse.File("pkg/p/big.go", src)
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	if len(syms) != 1 || syms[0].Kind != parse.KindStruct {
		t.Fatalf("symbols = %+v, want one struct", syms)
	}
	sig := syms[0].Signature
	if len(sig) > 200 {
		t.Errorf("signature length = %d, want <= 200", len(sig))
	}
	if strings.Contains(sig, "Hidden") {
		t.Errorf("signature %q contains the field body", sig)
	}
	if !strings.HasPrefix(sig, "type Big[") {
		t.Errorf("signature = %q, want a `type Big[` header", sig)
	}
}

// TestFileReceiverAndDoc pins the details the golden also covers, so a
// regression names the broken field directly.
func TestFileReceiverAndDoc(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "sample.go"))
	if err != nil {
		t.Fatal(err)
	}
	syms, err := parse.File("pkg/scheduler/sample.go", src)
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	byName := make(map[string]parse.Symbol, len(syms))
	for _, s := range syms {
		byName[s.Name] = s
	}

	pointer := byName["PointerReceiver"]
	if pointer.Kind != parse.KindMethod {
		t.Errorf("PointerReceiver kind = %q, want method", pointer.Kind)
	}
	if pointer.Receiver != "*Scheduler" {
		t.Errorf("PointerReceiver receiver = %q, want *Scheduler", pointer.Receiver)
	}
	if want := "k8s.io/kubernetes/pkg/scheduler.(*Scheduler).PointerReceiver"; pointer.QualifiedName != want {
		t.Errorf("PointerReceiver qualified name = %q, want %q", pointer.QualifiedName, want)
	}
	if want := "func (s *Scheduler) PointerReceiver(ctx context.Context) (ScheduleResult, error)"; pointer.Signature != want {
		t.Errorf("PointerReceiver signature = %q, want %q", pointer.Signature, want)
	}

	value := byName["ValueReceiver"]
	if value.Receiver != "Scheduler" {
		t.Errorf("ValueReceiver receiver = %q, want Scheduler", value.Receiver)
	}
	if want := "k8s.io/kubernetes/pkg/scheduler.(Scheduler).ValueReceiver"; value.QualifiedName != want {
		t.Errorf("ValueReceiver qualified name = %q, want %q", value.QualifiedName, want)
	}

	documented := byName["Documented"]
	if want := "// Documented has a two-part doc comment.\n//\n// The second paragraph is behind an empty comment line."; documented.Doc != want {
		t.Errorf("Documented doc = %q, want %q", documented.Doc, want)
	}
	if unnamed := byName["Unnamed"]; unnamed.Doc != "" {
		t.Errorf("Unnamed doc = %q, want empty (blank line gap)", unnamed.Doc)
	}
}

// TestAssignQualifiedNamesCrossFile pins the #n convention: duplicates ordered
// by (path, start_line) across a package; the first occurrence keeps the plain
// name, the second gets #1, the third #2, and so on.
func TestAssignQualifiedNamesCrossFile(t *testing.T) {
	srcA := []byte("package pkg\n\nfunc init() {}\nfunc init() {}\nfunc Foo() {}\n")
	srcB := []byte("package pkg\n\nfunc init() {}\nfunc init() {}\n")
	a, err := parse.File("pkg/a.go", srcA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := parse.File("pkg/b.go", srcB)
	if err != nil {
		t.Fatal(err)
	}

	files := []parse.FileSymbols{
		{Path: "pkg/a.go", Symbols: a},
		{Path: "pkg/b.go", Symbols: b},
	}
	parse.AssignQualifiedNames(files)

	var got []string
	for _, f := range files {
		for _, s := range f.Symbols {
			got = append(got, s.QualifiedName)
		}
	}
	want := []string{
		"k8s.io/kubernetes/pkg.init",
		"k8s.io/kubernetes/pkg.init#1",
		"k8s.io/kubernetes/pkg.Foo",
		"k8s.io/kubernetes/pkg.init#2",
		"k8s.io/kubernetes/pkg.init#3",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d names, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("name[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestFileSyntaxError proves a file with tree-sitter ERROR nodes still yields
// the symbols that parsed cleanly and reports ErrSyntax, so the caller counts
// the file as a parse error but still stores it.
func TestFileSyntaxError(t *testing.T) {
	src := []byte("package pkg\n\nfunc Good() {}\n\nfunc Bad( {\n")
	syms, err := parse.File("pkg/bad.go", src)
	if !errors.Is(err, parse.ErrSyntax) {
		t.Fatalf("err = %v, want ErrSyntax", err)
	}
	found := false
	for _, s := range syms {
		if s.Name == "Good" {
			found = true
		}
	}
	if !found {
		t.Errorf("symbols = %+v, want the cleanly parsed Good to survive", syms)
	}
}
