package chunk

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
)

// TestSplitIdentifiers pins the exact token lists from the chunker contract:
// the lowercased original first, then left-to-right parts split on camel,
// acronym, letter/digit and separator boundaries, with length-1 non-digit
// tokens dropped and duplicates removed.
func TestSplitIdentifiers(t *testing.T) {
	cases := []struct {
		word string
		want []string
	}{
		{"schedulePod", []string{"schedulepod", "schedule", "pod"}},
		{"HTTPServer", []string{"httpserver", "http", "server"}},
		{"pod_backoff", []string{"pod_backoff", "pod", "backoff"}},
		{"foo.bar", []string{"foo.bar", "foo", "bar"}},
		{"foo/bar", []string{"foo/bar", "foo", "bar"}},
		{"foo:bar", []string{"foo:bar", "foo", "bar"}},
		{"foo-bar", []string{"foo-bar", "foo", "bar"}},
		{"foo bar", []string{"foo bar", "foo", "bar"}},
		{"utf8", []string{"utf8", "utf", "8"}},
		{"pod", []string{"pod"}},
		{"2", []string{"2"}},
		{"a", nil},
		{"A", nil},
		{"", nil},
	}
	for _, tc := range cases {
		got := SplitIdentifiers(tc.word)
		if len(got) == 0 && len(tc.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("SplitIdentifiers(%q) = %v, want %v", tc.word, got, tc.want)
		}
	}
}

// TestSplitIdentifiersNoPanic feeds odd inputs and only requires no panic: the
// tokenizer must be total on unicode, punctuation and separators.
func TestSplitIdentifiersNoPanic(t *testing.T) {
	for _, word := range []string{
		"日本語", "hélloWörld", "!!!", "___", "  ", "-", "/", ":", ".",
		"a1B2c3D4", "  mixed_Case/Path:name-x.go  ", "𝕏", "\x00",
	} {
		_ = SplitIdentifiers(word)
	}
}

// TestSplitFileSpans pins the doc-inclusive line span and content join, and
// proves a bad span is rejected rather than stored.
func TestSplitFileSpans(t *testing.T) {
	src := []string{
		"package p",       // 1
		"",                // 2
		"// Doc line one", // 3
		"// Doc line two", // 4
		"func F() {}",     // 5
		"",                // 6
		"func G() {}",     // 7
	}
	syms := []Symbol{
		{ID: 1, FileID: 10, Kind: "func", Name: "F", StartLine: 5, EndLine: 5, Doc: "// Doc line one\n// Doc line two"},
		{ID: 2, FileID: 10, Kind: "func", Name: "G", StartLine: 7, EndLine: 7},
	}
	got := SplitFile(syms, src, parse.Facts{})
	if len(got) != 2 {
		t.Fatalf("chunk count = %d, want 2", len(got))
	}
	want := Chunk{
		SymbolID: 1, FileID: 10, PartIdx: 0, StartLine: 3, EndLine: 5,
		Content:     "// Doc line one\n// Doc line two\nfunc F() {}",
		ContentHash: ContentHash("", "// Doc line one\n// Doc line two\nfunc F() {}"),
		TokenCount:  EstimateTokens("// Doc line one\n// Doc line two\nfunc F() {}"),
	}
	if got[0] != want {
		t.Errorf("chunk[0] = %+v, want %+v", got[0], want)
	}
	if got[1].StartLine != 7 || got[1].EndLine != 7 || got[1].Content != "func G() {}" {
		t.Errorf("chunk[1] = %+v, want span 7..7 content %q", got[1], "func G() {}")
	}

	for _, bad := range []struct {
		name string
		sym  Symbol
	}{
		{"start below one", Symbol{ID: 3, StartLine: 0, EndLine: 2, Doc: "// a"}},
		{"end before start", Symbol{ID: 4, StartLine: 5, EndLine: 4}},
		{"beyond source", Symbol{ID: 5, StartLine: 6, EndLine: 99}},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: SplitFile did not reject the bad span", bad.name)
				}
			}()
			SplitFile([]Symbol{bad.sym}, src, parse.Facts{})
		}()
	}
}

// longFunctionSource builds a documented function of n statements, each wide
// enough that the whole function is over the ceiling.
func longFunctionSource(n int) ([]string, []parse.LineRange) {
	lines := []string{
		"package p",
		"",
		"// Doc",
		"func Long() {",
	}
	ranges := make([]parse.LineRange, 0, n)
	for i := 0; i < n; i++ {
		lines = append(lines, fmt.Sprintf("\ts%d := \"%s\"", i, strings.Repeat("x", 300)))
		ranges = append(ranges, parse.LineRange{StartLine: len(lines), EndLine: len(lines)})
	}
	lines = append(lines, "}")
	return lines, ranges
}

// TestSplitFileParts proves an over-ceiling function is split at top-level
// statement boundaries: every part starts with the header (and part 0 with the
// doc), carries the part marker, stays within the ceiling, and no statement is
// cut across parts.
func TestSplitFileParts(t *testing.T) {
	lines, ranges := longFunctionSource(20)
	sym := Symbol{ID: 1, FileID: 2, Kind: "func", Name: "Long", Signature: "func Long()", StartLine: 4, EndLine: len(lines), Doc: "// Doc"}
	parts := SplitFile([]Symbol{sym}, lines, parse.Facts{Statements: map[int][]parse.LineRange{4: ranges}})
	if len(parts) < 2 {
		t.Fatalf("part count = %d, want at least 2", len(parts))
	}

	header := "func Long() {"
	doc := strings.Join(lines[sym.StartLine-2:sym.StartLine-1], "\n")
	seen := make(map[int]bool)
	for i, p := range parts {
		wantPrefix := header + "\n" + fmt.Sprintf("// ... part %d/%d", i+1, len(parts)) + "\n"
		if i == 0 {
			wantPrefix = doc + "\n" + wantPrefix
		}
		if !strings.HasPrefix(p.Content, wantPrefix) {
			t.Errorf("part %d does not start with %q:\n%q", i, wantPrefix, p.Content)
		}
		if p.PartIdx != i {
			t.Errorf("part_idx = %d, want %d", p.PartIdx, i)
		}
		if i > 0 {
			wantTail := strings.Join(lines[p.StartLine-1:p.EndLine], "\n")
			if !strings.HasSuffix(p.Content, wantTail) {
				t.Errorf("part %d does not end with lines %d..%d", i, p.StartLine, p.EndLine)
			}
		}
		if p.StartLine < sym.StartLine-1 || p.EndLine > sym.EndLine || p.EndLine < p.StartLine {
			t.Errorf("part %d span %d..%d outside %d..%d", i, p.StartLine, p.EndLine, sym.StartLine-1, sym.EndLine)
		}
		if p.TokenCount > ChunkMaxTokens {
			t.Errorf("part %d token_count = %d, want <= %d", i, p.TokenCount, ChunkMaxTokens)
		}
		if p.TokenCount != EstimateTokens(p.Content) {
			t.Errorf("part %d token_count = %d, want EstimateTokens = %d", i, p.TokenCount, EstimateTokens(p.Content))
		}
		if p.ContentHash != ContentHash("", p.Content) {
			t.Errorf("part %d content_hash mismatch", i)
		}
		// Every statement falls wholly inside exactly one part.
		for _, r := range ranges {
			body := strings.Join(lines[r.StartLine-1:r.EndLine], "\n")
			if strings.Contains(p.Content, body) {
				if seen[r.StartLine] {
					t.Errorf("statement at line %d appears in more than one part", r.StartLine)
				}
				seen[r.StartLine] = true
			}
		}
	}
	if len(seen) != len(ranges) {
		t.Errorf("statements covered = %d, want %d", len(seen), len(ranges))
	}
	// A boundary never falls inside a statement: the first line of every part
	// after part 0 is a statement start.
	starts := make(map[int]bool)
	for _, r := range ranges {
		starts[r.StartLine] = true
	}
	for _, p := range parts[1:] {
		if !starts[p.StartLine] {
			t.Errorf("part starts at line %d, not a statement boundary", p.StartLine)
		}
	}
}

// TestSplitFileOversizedStatement proves one statement over the ceiling stays
// whole as its own part, is still marked, and is not split mid-statement.
func TestSplitFileOversizedStatement(t *testing.T) {
	body := "\t_ = \"" + strings.Repeat("x", 5000) + "\""
	lines := []string{"package p", "func Giant() {", body, "}"}
	sym := Symbol{ID: 3, FileID: 4, Kind: "func", Name: "Giant", Signature: "func Giant()", StartLine: 2, EndLine: 4}
	parts := SplitFile([]Symbol{sym}, lines, parse.Facts{Statements: map[int][]parse.LineRange{2: {{StartLine: 3, EndLine: 3}}}})
	if len(parts) != 1 {
		t.Fatalf("part count = %d, want 1 (the oversized statement is one part)", len(parts))
	}
	p := parts[0]
	if p.PartIdx != 0 {
		t.Errorf("part_idx = %d, want 0", p.PartIdx)
	}
	if !strings.Contains(p.Content, "// ... part 1/1") {
		t.Errorf("oversized part is missing its marker:\n%q", p.Content)
	}
	if !strings.Contains(p.Content, body) {
		t.Errorf("oversized part does not contain the whole statement")
	}
	if p.TokenCount <= ChunkMaxTokens {
		t.Errorf("token_count = %d, want > %d", p.TokenCount, ChunkMaxTokens)
	}
	if p.EndLine != sym.EndLine {
		t.Errorf("last part end_line = %d, want %d", p.EndLine, sym.EndLine)
	}
}

// TestSplitFilePartsNeedStatements proves a symbol over the ceiling without
// statement ranges is rejected rather than split heuristically.
func TestSplitFilePartsNeedStatements(t *testing.T) {
	lines := []string{"package p", "func Long() {", "\t_ = \"" + strings.Repeat("x", 5000) + "\"", "}"}
	defer func() {
		if recover() == nil {
			t.Errorf("SplitFile did not reject an over-ceiling symbol without statements")
		}
	}()
	SplitFile([]Symbol{{ID: 5, StartLine: 2, EndLine: 4}}, lines, parse.Facts{})
}

// TestChunkedSHAIncludesParameters proves the marker hashes the file sha, both
// ceilings and the chunker version, so a change to any of them re-splits every
// file (D-078).
func TestChunkedSHAIncludesParameters(t *testing.T) {
	base := ChunkedSHA("abc")
	if base != ParamsSHA("abc", ChunkMaxTokens, GroupMaxTokens, ChunkerVersion) {
		t.Errorf("ChunkedSHA is not ParamsSHA with the production parameters")
	}
	for _, other := range []string{
		ParamsSHA("abd", ChunkMaxTokens, GroupMaxTokens, ChunkerVersion),
		ParamsSHA("abc", ChunkMaxTokens+1, GroupMaxTokens, ChunkerVersion),
		ParamsSHA("abc", ChunkMaxTokens, GroupMaxTokens+1, ChunkerVersion),
		ParamsSHA("abc", ChunkMaxTokens, GroupMaxTokens, ChunkerVersion+1),
	} {
		if other == base {
			t.Errorf("a parameter change left the marker unchanged")
		}
	}
	if len(base) != 64 {
		t.Errorf("ChunkedSHA length = %d, want 64 (sha256 hex)", len(base))
	}
}

// TestContentHashSeparatesFields proves the field order matters and that a
// split across the boundary cannot collide.
func TestContentHashSeparatesFields(t *testing.T) {
	if ContentHash("ab", "c") == ContentHash("a", "bc") {
		t.Errorf(`ContentHash("ab","c") collides with ("a","bc")`)
	}
	if ContentHash("h", "c") == ContentHash("c", "h") {
		t.Errorf("ContentHash ignores field order")
	}
	if len(ContentHash("h", "c")) != 64 {
		t.Errorf("ContentHash is not sha256 hex")
	}
}

// TestLexicalTextNonEmpty proves the lexical text is non-empty whenever the
// chunk has real content, and that it folds identifiers into searchable tokens.
func TestLexicalTextNonEmpty(t *testing.T) {
	if got := LexicalText("", "func UsesPod() { schedulePod() }"); got == "" {
		t.Error("LexicalText returned empty for real content")
	} else {
		found := false
		for _, tok := range strings.Fields(got) {
			if tok == "pod" {
				found = true
			}
		}
		if !found {
			t.Errorf("LexicalText(%q) = %q, want a pod token", "func UsesPod() { schedulePod() }", got)
		}
	}
	if got := LexicalText("", ""); got != "" {
		t.Errorf("LexicalText of empty input = %q, want empty", got)
	}
}

// FuzzSplit only asserts SplitIdentifiers never panics. The normal suite runs
// the seed corpus; the long fuzz run is manual.
func FuzzSplit(f *testing.F) {
	for _, s := range []string{"schedulePod", "HTTPServer", "pod_backoff", "utf8", "", "a", "日本語", "A_B/c:d-e"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_ = SplitIdentifiers(s)
	})
}

// TestSplitFileSharedDeclarationLine proves every part keeps a synthetic
// signature header even when the first statement starts on the declaration
// line, where the source gap between the declaration and the first statement is
// empty.
func TestSplitFileSharedDeclarationLine(t *testing.T) {
	lines := []string{
		"package p",
		`func OneLine() { s0 := "` + strings.Repeat("x", 300) + `"`,
	}
	ranges := []parse.LineRange{{StartLine: 2, EndLine: 2}}
	for i := 1; i < 20; i++ {
		lines = append(lines, fmt.Sprintf("\ts%d := \"%s\"", i, strings.Repeat("x", 300)))
		ranges = append(ranges, parse.LineRange{StartLine: len(lines), EndLine: len(lines)})
	}
	lines = append(lines, "}")
	sym := Symbol{
		ID: 7, FileID: 8, Kind: "func", Name: "OneLine",
		Signature: "func OneLine()", StartLine: 2, EndLine: len(lines),
	}
	parts := SplitFile([]Symbol{sym}, lines, parse.Facts{Statements: map[int][]parse.LineRange{2: ranges}})
	if len(parts) < 2 {
		t.Fatalf("part count = %d, want at least 2", len(parts))
	}
	for i, p := range parts {
		wantPrefix := "func OneLine() {\n" + fmt.Sprintf("// ... part %d/%d", i+1, len(parts)) + "\n"
		if !strings.HasPrefix(p.Content, wantPrefix) {
			t.Errorf("part %d lost the signature header:\ngot  %q\nwant prefix %q", i, p.Content, wantPrefix)
		}
	}
}
