package chunk

import (
	"reflect"
	"strings"
	"testing"
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
	got := SplitFile(syms, src)
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
			SplitFile([]Symbol{bad.sym}, src)
		}()
	}
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
