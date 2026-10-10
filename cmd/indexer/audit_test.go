package main

import (
	"fmt"
	"strings"
	"testing"
)

// fakeRows builds n candidates ordered by qualified name, as selectAuditRows
// returns them.
func fakeRows(n int) []auditRow {
	rows := make([]auditRow, n)
	for i := range rows {
		rows[i] = auditRow{QualifiedName: fmt.Sprintf("k8s.io/kubernetes/pkg/p.fn%03d", i)}
	}
	return rows
}

// TestSampleRowsDeterministic proves the same candidate list and seed give the
// same sample in the same order, and a different seed gives a different order.
func TestSampleRowsDeterministic(t *testing.T) {
	rows := fakeRows(100)

	a := sampleRows(rows, 30, 42)
	b := sampleRows(rows, 30, 42)
	if len(a) != 30 {
		t.Fatalf("sample size = %d, want 30", len(a))
	}
	for i := range a {
		if a[i].QualifiedName != b[i].QualifiedName {
			t.Fatalf("same seed differs at %d: %q vs %q", i, a[i].QualifiedName, b[i].QualifiedName)
		}
	}

	c := sampleRows(rows, 30, 7)
	same := true
	for i := range a {
		if a[i].QualifiedName != c[i].QualifiedName {
			same = false
			break
		}
	}
	if same {
		t.Error("seed 7 and seed 42 produced the same sample")
	}
}

// TestSampleRowsClamps proves requesting more rows than exist returns them all
// and never panics.
func TestSampleRowsClamps(t *testing.T) {
	if got := sampleRows(fakeRows(3), 30, 42); len(got) != 3 {
		t.Errorf("sample size = %d, want 3", len(got))
	}
}

// auditSource is a small file used by the checkSymbol and docAbove tests.
var auditSource = strings.Join([]string{
	"package a",                  // 1
	"",                           // 2
	"// Foo does a thing.",       // 3
	"func Foo(x int) {",          // 4
	"\t_ = x",                    // 5
	"}",                          // 6
	"",                           // 7
	"// Bar does another thing.", // 8
	"func (s *Served) Bar() {",   // 9
	"}",                          // 10
}, "\n")

// TestCheckSymbol proves a consistent row passes and each of the three checks
// fails with a reason when the stored value is wrong.
func TestCheckSymbol(t *testing.T) {
	src := []byte(auditSource)
	foo := auditRow{
		Name:      "Foo",
		StartLine: 4, EndLine: 6, Doc: "// Foo does a thing.",
	}
	bar := auditRow{
		Name: "Bar", Receiver: "*Served",
		StartLine: 9, EndLine: 10, Doc: "// Bar does another thing.",
	}
	for name, row := range map[string]auditRow{"func": foo, "method": bar} {
		if reason := checkSymbol(row, src); reason != "" {
			t.Errorf("%s: unexpected mismatch: %s", name, reason)
		}
	}

	badName := foo
	badName.Name = "Missing"
	if reason := checkSymbol(badName, src); !strings.Contains(reason, "does not declare name") {
		t.Errorf("bad name reason = %q", reason)
	}
	badDoc := foo
	badDoc.Doc = "// wrong"
	if reason := checkSymbol(badDoc, src); !strings.Contains(reason, "doc mismatch") {
		t.Errorf("bad doc reason = %q", reason)
	}
	badRecv := bar
	badRecv.Receiver = "*Other"
	if reason := checkSymbol(badRecv, src); !strings.Contains(reason, "receiver mismatch") {
		t.Errorf("bad receiver reason = %q", reason)
	}
}

// TestDocAbove proves the doc span matches parse's contiguous // rule,
// including a run with a blank-line gap and a declaration at the file start.
func TestDocAbove(t *testing.T) {
	lines := strings.Split(auditSource, "\n")
	if got := docAbove(lines, 4); got != "// Foo does a thing." {
		t.Errorf("doc above line 4 = %q", got)
	}
	if got := docAbove(lines, 9); got != "// Bar does another thing." {
		t.Errorf("doc above line 9 = %q", got)
	}
	if got := docAbove(lines, 1); got != "" {
		t.Errorf("doc above line 1 = %q, want empty", got)
	}
}
