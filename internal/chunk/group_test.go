package chunk

import (
	"strings"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
)

// groupedSource is a fixture covering the group rules: a struct and an interface
// each with a same-file constructor, a plain struct, a type whose first of two
// candidate constructors groups, and a function that is a constructor by return
// type only.
func groupedSource() []string {
	return strings.Split(`package p

// Gadget doc
type Gadget struct {
}

func NewGadget() *Gadget {
	return &Gadget{}
}

// Solo doc
type Solo struct{}

type Iface interface {
}

func NewIface() Iface {
	return nil
}

type Twin struct{}

func NewTwin() *Twin { return &Twin{} }

func MakeTwin() *Twin { return &Twin{} }

type X struct{}

func buildX() *X { return &X{} }
`, "\n")
}

func groupedSymbols() []Symbol {
	return []Symbol{
		{ID: 1, FileID: 9, Kind: parse.KindStruct, Name: "Gadget", StartLine: 4, EndLine: 5, Doc: "// Gadget doc"},
		{ID: 2, FileID: 9, Kind: parse.KindFunc, Name: "NewGadget", StartLine: 7, EndLine: 9, Signature: "func NewGadget() *Gadget"},
		{ID: 3, FileID: 9, Kind: parse.KindStruct, Name: "Solo", StartLine: 12, EndLine: 12, Doc: "// Solo doc"},
		{ID: 4, FileID: 9, Kind: parse.KindInterface, Name: "Iface", StartLine: 14, EndLine: 15},
		{ID: 5, FileID: 9, Kind: parse.KindFunc, Name: "NewIface", StartLine: 17, EndLine: 19, Signature: "func NewIface() Iface"},
		{ID: 6, FileID: 9, Kind: parse.KindStruct, Name: "Twin", StartLine: 21, EndLine: 21},
		{ID: 7, FileID: 9, Kind: parse.KindFunc, Name: "NewTwin", StartLine: 23, EndLine: 23, Signature: "func NewTwin() *Twin"},
		{ID: 8, FileID: 9, Kind: parse.KindFunc, Name: "MakeTwin", StartLine: 25, EndLine: 25, Signature: "func MakeTwin() *Twin"},
		{ID: 9, FileID: 9, Kind: parse.KindStruct, Name: "X", StartLine: 27, EndLine: 27},
		{ID: 10, FileID: 9, Kind: parse.KindFunc, Name: "buildX", StartLine: 29, EndLine: 29, Signature: "func buildX() *X"},
	}
}

func groupedResults() map[int]string {
	return map[int]string{
		7:  "*Gadget",
		17: "Iface",
		23: "*Twin",
		25: "*Twin",
		29: "*X",
	}
}

// TestSplitFileGroups proves the group rules pure: a struct or interface with a
// same-file constructor yields exactly ONE chunk owned by the type, spanning
// both declarations (including the lines between), with the constructor getting
// no chunk; a plain type keeps one chunk; the first candidate of two groups; and
// a function is a constructor by return type alone.
func TestSplitFileGroups(t *testing.T) {
	lines := groupedSource()
	chunks := SplitFile(groupedSymbols(), lines, parse.Facts{Results: groupedResults()})

	byID := map[int64]Chunk{}
	for _, c := range chunks {
		byID[c.SymbolID] = c
	}
	want := []struct {
		id    int64
		start int
		end   int
	}{
		{1, 3, 9},   // Gadget grouped with NewGadget (doc + blank + doc/body)
		{3, 11, 12}, // Solo: own chunk, doc included
		{4, 14, 19}, // Iface grouped with NewIface
		{6, 21, 23}, // Twin grouped with NewTwin (the first candidate)
		{8, 25, 25}, // MakeTwin: second candidate keeps its own chunk
		{9, 27, 29}, // X grouped with buildX by return type
	}
	if len(chunks) != len(want) {
		t.Fatalf("chunk count = %d, want %d: %+v", len(chunks), len(want), chunks)
	}
	for _, w := range want {
		c, ok := byID[w.id]
		if !ok {
			t.Errorf("symbol %d has no chunk", w.id)
			continue
		}
		if c.PartIdx != 0 {
			t.Errorf("symbol %d part_idx = %d, want 0", w.id, c.PartIdx)
		}
		if c.StartLine != w.start || c.EndLine != w.end {
			t.Errorf("symbol %d span = %d..%d, want %d..%d", w.id, c.StartLine, c.EndLine, w.start, w.end)
		}
		if got, wantContent := c.Content, strings.Join(lines[w.start-1:w.end], "\n"); got != wantContent {
			t.Errorf("symbol %d content mismatch\ngot  %q\nwant %q", w.id, got, wantContent)
		}
		if c.ContentHash != ContentHash("", c.Content) {
			t.Errorf("symbol %d content_hash mismatch", w.id)
		}
		if c.TokenCount != EstimateTokens(c.Content) {
			t.Errorf("symbol %d token_count = %d, want %d", w.id, c.TokenCount, EstimateTokens(c.Content))
		}
	}
	for _, groupedAway := range []int64{2, 5, 7, 10} {
		if _, ok := byID[groupedAway]; ok {
			t.Errorf("grouped constructor %d produced a chunk", groupedAway)
		}
	}
}

// TestSplitFileGroupCeiling proves a pair over GroupMaxTokens is NOT grouped:
// the type keeps its own chunk and the constructor keeps its own.
func TestSplitFileGroupCeiling(t *testing.T) {
	body := "\tData: \"" + strings.Repeat("x", 2400) + "\","
	lines := []string{
		"package p",            // 1
		"",                     // 2
		"type Big struct {",    // 3
		"\tData string",        // 4
		"}",                    // 5
		"",                     // 6
		"func NewBig() *Big {", // 7
		"\treturn &Big{",       // 8
		body,                   // 9
		"\t}",                  // 10
		"}",                    // 11
	}
	syms := []Symbol{
		{ID: 1, FileID: 2, Kind: parse.KindStruct, Name: "Big", StartLine: 3, EndLine: 5},
		{ID: 2, FileID: 2, Kind: parse.KindFunc, Name: "NewBig", StartLine: 7, EndLine: 11, Signature: "func NewBig() *Big"},
	}
	facts := parse.Facts{Results: map[int]string{7: "*Big"}}
	pair := strings.Join(lines[2:11], "\n")
	if EstimateTokens(pair) <= GroupMaxTokens {
		t.Fatalf("pair estimate = %d, want > %d (fixture is not over the group ceiling)",
			EstimateTokens(pair), GroupMaxTokens)
	}
	chunks := SplitFile(syms, lines, facts)
	if len(chunks) != 2 {
		t.Fatalf("chunk count = %d, want 2 (type + constructor)", len(chunks))
	}
	if chunks[0].SymbolID != 1 || chunks[0].StartLine != 3 || chunks[0].EndLine != 5 {
		t.Errorf("type chunk = %+v, want its own span 3..5", chunks[0])
	}
	if chunks[1].SymbolID != 2 || chunks[1].StartLine != 7 || chunks[1].EndLine != 11 {
		t.Errorf("constructor chunk = %+v, want its own span 7..11", chunks[1])
	}
}

// TestSplitFileGroupOneFunctionOneGroup proves a function matched by two types
// (by name and by return type) joins exactly one group: the earlier type claims
// it, later types cannot reuse it, and the function owns no chunk of its own.
func TestSplitFileGroupOneFunctionOneGroup(t *testing.T) {
	lines := strings.Split(`package p

type A struct{}

func NewB() *A {
	return &A{}
}

type B struct{}
`, "\n")
	syms := []Symbol{
		{ID: 1, FileID: 1, Kind: parse.KindStruct, Name: "A", StartLine: 3, EndLine: 3},
		{ID: 2, FileID: 1, Kind: parse.KindFunc, Name: "NewB", StartLine: 5, EndLine: 7, Signature: "func NewB() *A"},
		{ID: 3, FileID: 1, Kind: parse.KindStruct, Name: "B", StartLine: 9, EndLine: 9},
	}
	facts := parse.Facts{Results: map[int]string{5: "*A"}}
	chunks := SplitFile(syms, lines, facts)

	for _, c := range chunks {
		if c.SymbolID == 2 {
			t.Errorf("NewB owns a chunk of its own: %+v", c)
		}
	}
	body := strings.Join(lines[4:7], "\n")
	if hits := countContaining(chunks, body); hits != 1 {
		t.Errorf("NewB body appears in %d chunks, want exactly 1", hits)
	}
	for i := range chunks {
		for j := i + 1; j < len(chunks); j++ {
			if chunks[i].StartLine <= chunks[j].EndLine && chunks[j].StartLine <= chunks[i].EndLine {
				t.Errorf("chunks overlap:\n%+v\n%+v", chunks[i], chunks[j])
			}
		}
	}
}

// countContaining returns how many chunks contain s.
func countContaining(chunks []Chunk, s string) int {
	n := 0
	for _, c := range chunks {
		if strings.Contains(c.Content, s) {
			n++
		}
	}
	return n
}

// TestSplitFileTypeOverCeilingStaysWhole proves a struct or interface over
// ChunkMaxTokens is one whole chunk, never split into parts.
func TestSplitFileTypeOverCeilingStaysWhole(t *testing.T) {
	doc := "// " + strings.Repeat("x", 5000)
	lines := []string{"package p", "", doc, "type Huge struct {", "\tF string", "}"}
	sym := Symbol{ID: 1, FileID: 2, Kind: parse.KindStruct, Name: "Huge", StartLine: 4, EndLine: 6, Doc: doc}
	chunks := SplitFile([]Symbol{sym}, lines, parse.Facts{})
	if len(chunks) != 1 {
		t.Fatalf("chunk count = %d, want 1 (a type is never split)", len(chunks))
	}
	c := chunks[0]
	if c.PartIdx != 0 {
		t.Errorf("part_idx = %d, want 0", c.PartIdx)
	}
	if c.TokenCount <= ChunkMaxTokens {
		t.Errorf("token_count = %d, want > %d (over the ceiling, kept whole)", c.TokenCount, ChunkMaxTokens)
	}
	if want := strings.Join(lines[2:6], "\n"); c.Content != want {
		t.Errorf("content mismatch\ngot  %q\nwant %q", c.Content, want)
	}
}
