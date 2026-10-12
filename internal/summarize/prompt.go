package summarize

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
)

// PromptVersion is the current function-summary prompt version. It is the only
// place this string appears; summarize-status, embedding (S3, S5) and
// coarse-to-fine retrieval all filter on it (D-068).
const PromptVersion = "fn-v1"

// SystemPrompt is the exact system message of the fn-v1 prompt.
const SystemPrompt = "You are summarizing a Go function from Kubernetes for a code search index. Write 1-2 sentences describing what it does and why. No code, no preamble."

// maxCodeChars caps the code excerpt in the user message at this many
// characters (runes). A longer excerpt is cut and marked with truncatedMarker.
const maxCodeChars = 6000

// truncatedMarker marks a code excerpt cut at maxCodeChars.
const truncatedMarker = "// ...truncated"

// Input is the pure prompt material for one function summary: the code excerpt,
// the declaration signature and doc, and the raw call names (D-014).
type Input struct {
	Signature string
	Doc       string
	RawCalls  []string
	Code      string
}

// BuildInput builds the prompt Input for sym by reading its code from relPath
// under kubeDir (lines StartLine..EndLine inclusive). It returns an error only
// if the file cannot be read.
func BuildInput(kubeDir, relPath string, sym parse.Symbol) (Input, error) {
	code, err := readCode(kubeDir, relPath, sym.StartLine, sym.EndLine)
	if err != nil {
		return Input{}, err
	}
	return Input{
		Signature: sym.Signature,
		Doc:       sym.Doc,
		RawCalls:  sym.RawCalls,
		Code:      code,
	}, nil
}

// UserMessage renders the user message: the signature, the doc, the raw call
// names and the code excerpt, separated by blank lines. An empty doc is
// omitted.
func (in Input) UserMessage() string {
	parts := []string{in.Signature}
	if in.Doc != "" {
		parts = append(parts, in.Doc)
	}
	parts = append(parts, "Calls (raw): "+strings.Join(in.RawCalls, ", "))
	parts = append(parts, in.Code)
	return strings.Join(parts, "\n\n")
}

// Hash is input_hash: sha256 over the current PromptVersion and, in order, the
// code, signature, doc and raw calls. Every field is followed by a NUL byte,
// and raw calls are joined by a NUL byte, so the encoding is unambiguous. A
// change to any field changes the hash (D-014).
func (in Input) Hash() string {
	h := sha256.New()
	fields := []string{PromptVersion, in.Code, in.Signature, in.Doc, strings.Join(in.RawCalls, "\x00")}
	for _, f := range fields {
		h.Write([]byte(f))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// readCode returns lines start..end (1-based, inclusive) of relPath under
// kubeDir, cut to maxCodeBytes and marked with truncatedMarker when cut.
func readCode(kubeDir, relPath string, start, end int) (string, error) {
	src, err := os.ReadFile(filepath.Join(kubeDir, relPath))
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(src), "\n")
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return "", nil
	}
	code := strings.Join(lines[start-1:end], "\n")
	if utf8.RuneCountInString(code) > maxCodeChars {
		code = string([]rune(code)[:maxCodeChars]) + "\n" + truncatedMarker
	}
	return code, nil
}
