package chunk

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

// ChunkerVersion changes whenever the splitting logic changes, so every file's
// chunk marker changes and the whole corpus is re-split (D-078).
const ChunkerVersion = 1

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

// Chunk is one embeddable unit. In this version every function and method is a
// single chunk (PartIdx 0); struct/interface/group chunks and parts come later.
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

// SplitFile returns one chunk per function or method symbol, in the given
// order. The doc comment lines are part of the content, so a chunk starts at
// symbol.StartLine minus the doc line count (D-083). It panics on a bad span,
// so a malformed symbol can never reach the chunks table.
func SplitFile(symbols []Symbol, src []string) []Chunk {
	out := make([]Chunk, 0, len(symbols))
	for _, sym := range symbols {
		n := 0
		if sym.Doc != "" {
			n = strings.Count(sym.Doc, "\n") + 1
		}
		start := sym.StartLine - n
		end := sym.EndLine
		if start < 1 || end < start || end > len(src) {
			panic(fmt.Sprintf("chunk: bad span for symbol %d (%s): start %d end %d, source has %d lines",
				sym.ID, sym.Name, start, end, len(src)))
		}
		content := strings.Join(src[start-1:end], "\n")
		out = append(out, Chunk{
			SymbolID:    sym.ID,
			FileID:      sym.FileID,
			PartIdx:     0,
			StartLine:   start,
			EndLine:     end,
			TokenCount:  EstimateTokens(content),
			Content:     content,
			ContentHash: ContentHash("", content),
		})
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
