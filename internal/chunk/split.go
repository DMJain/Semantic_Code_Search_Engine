package chunk

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
)

// ChunkerVersion changes whenever the splitting logic changes, so every file's
// chunk marker changes and the whole corpus is re-split (D-078).
const ChunkerVersion = 3

// Symbol is the subset of a parsed symbol the chunker needs: its identity, its
// declaration span and its leading doc comment (the D-083 parse rule).
type Symbol struct {
	ID        int64
	FileID    int64
	Kind      string
	Name      string
	Receiver  string
	Signature string
	StartLine int
	EndLine   int
	Doc       string
}

// Chunk is one embeddable unit: a whole struct or interface, a type-with-
// constructor group, or one part of a function or method split at top-level
// statement boundaries.
type Chunk struct {
	SymbolID    int64
	FileID      int64
	PartIdx     int
	StartLine   int
	EndLine     int
	TokenCount  int
	Content     string
	ContentHash string
}

// ChunkedSHA is the marker stored in files.chunked_sha: the file content sha
// hashed together with the production chunker parameters (D-078).
func ChunkedSHA(fileSHA string) string {
	return ParamsSHA(fileSHA, ChunkMaxTokens, GroupMaxTokens, ChunkerVersion)
}

// ParamsSHA is ChunkedSHA with explicit parameters, so a test can prove that a
// ceiling or version change changes every file's marker (D-078).
func ParamsSHA(fileSHA string, chunkMax, groupMax, version int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%d", fileSHA, chunkMax, groupMax, version)))
	return hex.EncodeToString(sum[:])
}

// ContentHash is sha256 hex of the header and content, separated so a value
// split across the boundary cannot collide. It is empty-header for a raw chunk
// and later header-bearing for a header chunk (D-081).
func ContentHash(header, content string) string {
	sum := sha256.Sum256([]byte(header + "\x00" + content))
	return hex.EncodeToString(sum[:])
}

// SplitFile returns chunks for every struct, interface, function and method
// symbol, in the given order.
//
// A struct or interface gets exactly one chunk — never split, even over
// ChunkMaxTokens — spanning its doc comment start through its end line. When the
// type has a same-file constructor (a function named New<Name> or new<Name>, or
// one whose single declared result type is the type or a pointer to it), the pair
// becomes one group chunk owned by the type, spanning the first declaration's doc
// start through the last's end line, only while the whole span is within
// GroupMaxTokens; the constructor then gets no chunk of its own. A function is
// claimed by at most one group.
//
// A function or method whose whole content fits ChunkMaxTokens produces exactly
// one part identical to the source span (D-083). A larger one is split at the
// top-level statement ranges in facts.Statements, keyed by the symbol's start
// line, into greedy-filled parts with a signature header and a `// ... part i/n`
// marker; a single oversized statement stays whole as its own part (D-078,
// D-082). It panics on a bad span or a split symbol without statements, so a
// malformed symbol can never reach the chunks table.
func SplitFile(symbols []Symbol, src []string, facts parse.Facts) []Chunk {
	groups, groupedAway := planGroups(symbols, src, facts.Results)
	out := make([]Chunk, 0, len(symbols))
	for _, sym := range symbols {
		if groupedAway[sym.ID] {
			continue
		}
		if g, ok := groups[sym.ID]; ok {
			out = append(out, g)
			continue
		}
		if sym.Kind == parse.KindStruct || sym.Kind == parse.KindInterface {
			out = append(out, typeChunk(sym, src))
			continue
		}
		out = append(out, functionChunks(sym, src, facts.Statements)...)
	}
	return out
}

// planGroups returns, per type symbol id, its group chunk, and the set of
// constructor symbol ids that must produce no chunk. A type groups with the
// FIRST same-file function candidate in source order (name or return type) that
// no earlier type has already claimed, so one function can never join two
// groups; if the combined span is over GroupMaxTokens no grouping happens at
// all, so the type and every candidate keep their own chunk.
func planGroups(symbols []Symbol, src []string, results map[int]string) (map[int64]Chunk, map[int64]bool) {
	groups := make(map[int64]Chunk)
	groupedAway := make(map[int64]bool)
	for _, t := range symbols {
		if t.Kind != parse.KindStruct && t.Kind != parse.KindInterface {
			continue
		}
		for _, c := range symbols {
			if c.Kind != parse.KindFunc || groupedAway[c.ID] || !isConstructor(c, t, results) {
				continue
			}
			start := min(docStart(t), docStart(c))
			end := max(t.EndLine, c.EndLine)
			if start < 1 || end > len(src) || end < start {
				break
			}
			content := strings.Join(src[start-1:end], "\n")
			if EstimateTokens(content) > GroupMaxTokens {
				break
			}
			groups[t.ID] = Chunk{
				SymbolID:    t.ID,
				FileID:      t.FileID,
				PartIdx:     0,
				StartLine:   start,
				EndLine:     end,
				TokenCount:  EstimateTokens(content),
				Content:     content,
				ContentHash: ContentHash("", content),
			}
			groupedAway[c.ID] = true
			break
		}
	}
	return groups, groupedAway
}

// isConstructor reports whether c is a same-file constructor of t: a function
// named New<t> or new<t>, or one whose single declared result type is t or a
// pointer to t.
func isConstructor(c, t Symbol, results map[int]string) bool {
	if c.Name == "New"+t.Name || c.Name == "new"+t.Name {
		return true
	}
	rt := results[c.StartLine]
	return rt != "" && (rt == t.Name || rt == "*"+t.Name)
}

// docStart returns the first line of a symbol including its doc comment.
func docStart(sym Symbol) int {
	n := 0
	if sym.Doc != "" {
		n = strings.Count(sym.Doc, "\n") + 1
	}
	return sym.StartLine - n
}

// typeChunk builds the single, never-split chunk of a struct or interface: its
// doc comment start through its end line, exactly the source span. A type over
// ChunkMaxTokens is deliberately kept whole: D-078 defines splitting only for
// function bodies, so an over-ceiling type is reported as oversized and handled
// by the embed request budget, not split here.
func typeChunk(sym Symbol, src []string) Chunk {
	start, end := docStart(sym), sym.EndLine
	if start < 1 || end < start || end > len(src) {
		panic(fmt.Sprintf("chunk: bad span for symbol %d (%s): start %d end %d, source has %d lines",
			sym.ID, sym.Name, start, end, len(src)))
	}
	content := strings.Join(src[start-1:end], "\n")
	return Chunk{
		SymbolID:    sym.ID,
		FileID:      sym.FileID,
		PartIdx:     0,
		StartLine:   start,
		EndLine:     end,
		TokenCount:  EstimateTokens(content),
		Content:     content,
		ContentHash: ContentHash("", content),
	}
}

// functionChunks builds the single-or-parts chunks of one function or method. A
// symbol whose whole content fits ChunkMaxTokens produces exactly one part
// identical to the source span (D-083); a larger one splits at its statement
// ranges.
func functionChunks(sym Symbol, src []string, statements map[int][]parse.LineRange) []Chunk {
	start := docStart(sym)
	docLines := sym.StartLine - start
	end := sym.EndLine
	if start < 1 || end < start || end > len(src) {
		panic(fmt.Sprintf("chunk: bad span for symbol %d (%s): start %d end %d, source has %d lines",
			sym.ID, sym.Name, start, end, len(src)))
	}
	content := strings.Join(src[start-1:end], "\n")
	if EstimateTokens(content) <= ChunkMaxTokens {
		return []Chunk{{
			SymbolID:    sym.ID,
			FileID:      sym.FileID,
			PartIdx:     0,
			StartLine:   start,
			EndLine:     end,
			TokenCount:  EstimateTokens(content),
			Content:     content,
			ContentHash: ContentHash("", content),
		}}
	}
	ranges := statements[sym.StartLine]
	if len(ranges) == 0 {
		panic(fmt.Sprintf("chunk: symbol %d (%s) is over the ceiling but has no statement ranges",
			sym.ID, sym.Name))
	}
	return splitParts(sym, src, start, docLines, ranges)
}

// partHeader is the synthetic declaration header every part starts with: the
// stored signature with a trailing body brace. It is built from the signature
// text, not the source gap, because the first statement can share the
// declaration line, which would leave that gap empty (D-078). An empty
// signature falls back to the declaration's first source line.
func partHeader(sym Symbol, src []string) string {
	h := strings.TrimRight(sym.Signature, " \t\n")
	if h == "" {
		return src[sym.StartLine-1]
	}
	if !strings.HasSuffix(h, "{") {
		h += " {"
	}
	return h
}

// splitParts builds the parts of one over-ceiling symbol. Every part starts
// with the synthetic signature header; part 0 also carries the doc. Each part is
// one or more whole consecutive statements, and the last part extends to the
// symbol's closing line.
func splitParts(sym Symbol, src []string, docStart, docLines int, ranges []parse.LineRange) []Chunk {
	header := partHeader(sym, src)
	doc := ""
	if docLines > 0 {
		doc = strings.Join(src[docStart-1:sym.StartLine-1], "\n")
	}

	build := func(partIdx, total int, group []int) string {
		first := ranges[group[0]]
		last := ranges[group[len(group)-1]]
		end := last.EndLine
		if group[len(group)-1] == len(ranges)-1 {
			end = sym.EndLine
		}
		var b strings.Builder
		if partIdx == 0 && doc != "" {
			b.WriteString(doc)
			b.WriteByte('\n')
		}
		b.WriteString(header)
		b.WriteByte('\n')
		fmt.Fprintf(&b, "// ... part %d/%d", partIdx+1, total)
		b.WriteByte('\n')
		b.WriteString(strings.Join(src[first.StartLine-1:end], "\n"))
		return b.String()
	}

	partCount := len(ranges)
	var groups [][]int
	for {
		groups = groupStatements(len(ranges), partCount, build)
		if len(groups) == partCount {
			break
		}
		partCount = len(groups)
	}
	stmtCount := len(ranges)

	out := make([]Chunk, 0, len(groups))
	for i, group := range groups {
		content := build(i, len(groups), group)
		first := ranges[group[0]]
		last := ranges[group[len(group)-1]]
		start := first.StartLine
		if i == 0 {
			start = sym.StartLine
			if docLines > 0 {
				start = docStart
			}
		}
		end := last.EndLine
		if group[len(group)-1] == stmtCount-1 {
			end = sym.EndLine
		}
		out = append(out, Chunk{
			SymbolID:    sym.ID,
			FileID:      sym.FileID,
			PartIdx:     i,
			StartLine:   start,
			EndLine:     end,
			TokenCount:  EstimateTokens(content),
			Content:     content,
			ContentHash: ContentHash("", content),
		})
	}
	return out
}

// groupStatements greedy-fills consecutive statement indices into parts of at
// most ChunkMaxTokens, using partCount as the part count in the markers. A
// statement that does not fit is still placed in its own part, so a single
// oversized statement is never split.
func groupStatements(stmtCount, partCount int, build func(int, int, []int) string) [][]int {
	var groups [][]int
	for i := 0; i < stmtCount; {
		j := i
		for j+1 < stmtCount {
			if EstimateTokens(build(len(groups), partCount, seq(i, j+1))) > ChunkMaxTokens {
				break
			}
			j++
		}
		groups = append(groups, seq(i, j))
		i = j + 1
	}
	return groups
}

// seq returns the inclusive statement indices i..j.
func seq(i, j int) []int {
	out := make([]int, 0, j-i+1)
	for k := i; k <= j; k++ {
		out = append(out, k)
	}
	return out
}

// SplitIdentifiers splits one word into the lowercased original plus its
// camelCase, acronym, snake_case and digit-run parts. Separators (anything
// that is not a letter or digit, which includes "_ . / : -" and whitespace)
// split too. The lowercased original is kept first, parts follow left to right;
// tokens shorter than two runes are dropped unless they are a digit run, and
// duplicates are removed. The function is total: odd or unicode input never
// panics.
func SplitIdentifiers(word string) []string {
	lower := strings.ToLower(word)
	parts := splitIdentifiers(word)

	seen := make(map[string]bool, len(parts)+1)
	var out []string
	add := func(s string) {
		s = strings.ToLower(s)
		if s == "" || seen[s] {
			return
		}
		if len([]rune(s)) < 2 && !isDigitRun(s) {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	add(lower)
	for _, p := range parts {
		add(p)
	}
	return out
}

// splitIdentifiers returns the raw parts of word in source order, before length
// filtering and lowercasing.
func splitIdentifiers(word string) []string {
	var parts []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			parts = append(parts, string(cur))
			cur = cur[:0]
		}
	}
	for _, r := range word {
		switch runClass(r) {
		case classOther:
			flush()
			continue
		}
		if len(cur) == 0 {
			cur = append(cur, r)
			continue
		}
		prev := cur[len(cur)-1]
		// An acronym run followed by a lowercase letter splits before the last
		// uppercase, so HTTP|Server, not HTTP|S|erver.
		if runClass(prev) == classUpper && runClass(r) == classLower &&
			len(cur) >= 2 && runClass(cur[len(cur)-2]) == classUpper {
			last := cur[len(cur)-1]
			cur = cur[:len(cur)-1]
			flush()
			cur = append(cur, last, r)
			continue
		}
		if splits(prev, r) {
			flush()
		}
		cur = append(cur, r)
	}
	flush()
	return parts
}

// splits reports whether a boundary falls between prev and r.
func splits(prev, r rune) bool {
	pc, cc := runClass(prev), runClass(r)
	if pc == cc {
		return false
	}
	if pc == classDigit || cc == classDigit {
		return true
	}
	// Letters of different case: only lower->upper is a camel boundary; an
	// upper->lower run is either the start of a word or the acronym case,
	// which is handled before this call.
	return pc == classLower && cc == classUpper
}

// class groups runes for the boundary rules.
type class int

const (
	classOther class = iota
	classLower
	classUpper
	classDigit
)

func runClass(r rune) class {
	switch {
	case unicode.IsDigit(r):
		return classDigit
	case unicode.IsLower(r):
		return classLower
	case unicode.IsUpper(r):
		return classUpper
	default:
		return classOther
	}
}

// isDigitRun reports whether s is non-empty and every rune is a digit.
func isDigitRun(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// LexicalText builds the string whose Postgres `simple` parse becomes
// search_tsv: header plus content is split into letter/digit runs first, each
// run is split by SplitIdentifiers, and the tokens are joined with single
// spaces. Unlike SplitIdentifiers, a separator-joined compound word is not
// emitted as one token from content (only its parts are), because the runs
// already break on the separators. It is non-empty whenever the input has real
// content, so a stored chunk's tsvector is never empty.
func LexicalText(header, content string) string {
	var toks []string
	for _, w := range wordRuns(header + "\n" + content) {
		toks = append(toks, SplitIdentifiers(w)...)
	}
	if len(toks) == 0 {
		return strings.ToLower(strings.TrimSpace(header + " " + content))
	}
	return strings.Join(toks, " ")
}

// wordRuns returns the maximal runs of letters and digits in s.
func wordRuns(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = cur[:0]
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur = append(cur, r)
			continue
		}
		flush()
	}
	flush()
	return out
}
