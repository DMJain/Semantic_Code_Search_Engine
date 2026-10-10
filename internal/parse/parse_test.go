package parse_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
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
// the file as a parse error but still stores it (T07).
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
