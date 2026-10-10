package summarize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
)

// fixtureInput is the pinned prompt material used by the golden tests.
func fixtureInput() Input {
	return Input{
		Signature: "func add(a, b int) int",
		Doc:       "// add returns a + b.",
		RawCalls:  []string{"fmt.Sprintf", "strconv.Atoi"},
		Code:      "func add(a, b int) int {\n\treturn a + b\n}",
	}
}

func TestSystemPromptGolden(t *testing.T) {
	const want = "You are summarizing a Go function from Kubernetes for a code search index. Write 1-2 sentences describing what it does and why. No code, no preamble."
	if SystemPrompt != want {
		t.Errorf("SystemPrompt = %q\nwant %q", SystemPrompt, want)
	}
}

func TestUserMessageGolden(t *testing.T) {
	const want = "func add(a, b int) int\n\n" +
		"// add returns a + b.\n\n" +
		"Calls (raw): fmt.Sprintf, strconv.Atoi\n\n" +
		"func add(a, b int) int {\n\treturn a + b\n}"
	if got := fixtureInput().UserMessage(); got != want {
		t.Errorf("UserMessage() = %q\nwant %q", got, want)
	}
}

func TestUserMessageOmitsEmptyDoc(t *testing.T) {
	in := Input{Signature: "func f()", Code: "func f() {}"}
	got := in.UserMessage()
	if strings.Contains(got, "\n\n\n\n") || strings.HasPrefix(got, "\n") {
		t.Errorf("empty doc left blank sections: %q", got)
	}
	want := "func f()\n\nCalls (raw): \n\nfunc f() {}"
	if got != want {
		t.Errorf("UserMessage() = %q, want %q", got, want)
	}
}

func TestInputHashPinned(t *testing.T) {
	const want = "f54e09d4f6e512b3eeb07ddc2171158b5d226954dad22ca547516c3e467de77a"
	if got := fixtureInput().Hash(); got != want {
		t.Errorf("Hash() = %q, want pinned %q", got, want)
	}
}

func TestInputHashStableAndSensitive(t *testing.T) {
	base := fixtureInput()
	first := base.Hash()
	if second := base.Hash(); second != first {
		t.Fatalf("hash not stable: %q then %q", first, second)
	}
	changes := map[string]Input{
		"code":      {Signature: base.Signature, Doc: base.Doc, RawCalls: base.RawCalls, Code: base.Code + "\n"},
		"signature": {Signature: "func add(a, b int64) int", Doc: base.Doc, RawCalls: base.RawCalls, Code: base.Code},
		"doc":       {Signature: base.Signature, Doc: "// adds.", RawCalls: base.RawCalls, Code: base.Code},
		"raw_calls": {Signature: base.Signature, Doc: base.Doc, RawCalls: []string{"other"}, Code: base.Code},
	}
	for name, in := range changes {
		if in.Hash() == first {
			t.Errorf("changing %s did not change the hash", name)
		}
	}
}

func TestEligible(t *testing.T) {
	funcSym := func(start, end int) parse.Symbol {
		return parse.Symbol{Kind: parse.KindFunc, StartLine: start, EndLine: end}
	}
	cases := []struct {
		name    string
		sym     parse.Symbol
		inSlice bool
		want    bool
	}{
		{"two-line body excluded", funcSym(10, 12), true, false},
		{"three-line body included", funcSym(10, 13), true, true},
		{"not in slice", funcSym(10, 13), false, false},
		{"struct never eligible", parse.Symbol{Kind: parse.KindStruct, StartLine: 1, EndLine: 9}, true, false},
		{"method included", parse.Symbol{Kind: parse.KindMethod, StartLine: 1, EndLine: 4}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Eligible(tc.sym, tc.inSlice); got != tc.want {
				t.Errorf("Eligible() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildInputReadsLines(t *testing.T) {
	dir := t.TempDir()
	rel := "pkg/demo.go"
	content := "package demo\n\nfunc add(a, b int) int {\n\treturn a + b\n}\n"
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sym := parse.Symbol{
		Kind:      parse.KindFunc,
		Signature: "func add(a, b int) int",
		Doc:       "// add returns a + b.",
		StartLine: 3,
		EndLine:   5,
		RawCalls:  []string{"fmt.Sprintf"},
	}
	in, err := BuildInput(dir, rel, sym)
	if err != nil {
		t.Fatalf("BuildInput: %v", err)
	}
	want := "func add(a, b int) int {\n\treturn a + b\n}"
	if in.Code != want {
		t.Errorf("Code = %q, want %q", in.Code, want)
	}
	if in.Signature != sym.Signature || in.Doc != sym.Doc || len(in.RawCalls) != 1 {
		t.Errorf("input did not carry symbol fields: %+v", in)
	}
}

func TestBuildInputTruncatesLongCode(t *testing.T) {
	dir := t.TempDir()
	rel := "big.go"
	var b strings.Builder
	b.WriteString("package big\n")
	for i := 0; i < 2000; i++ {
		b.WriteString("\tx := 1234567890\n")
	}
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := BuildInput(dir, rel, parse.Symbol{StartLine: 1, EndLine: 2001})
	if err != nil {
		t.Fatalf("BuildInput: %v", err)
	}
	if !strings.HasSuffix(in.Code, truncatedMarker) {
		t.Errorf("long code not marked truncated: tail %q", tail(in.Code, 40))
	}
	if want := maxCodeChars + 1 + len(truncatedMarker); len(in.Code) != want {
		t.Errorf("len(Code) = %d, want %d", len(in.Code), want)
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
