package walk

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// File is one indexed Go file: a repo-root-relative slash path plus its Go
// import path, content hash and non-blank line count.
type File struct {
	Path    string
	Package string
	SHA     string
	LOC     int
	InSlice bool
}

// Stats summarizes a walk. Excluded holds one count per rule name (see
// RuleNames), including zeroes for rules that matched nothing.
type Stats struct {
	Indexed    int
	InSlice    int
	LOC        int
	InSliceLOC int
	Excluded   map[string]int
}

// Walk returns every indexed Go file under root, ordered by path, and the walk
// statistics. Only .go files are candidates; a candidate rejected by a rule is
// counted under that rule and not returned.
func Walk(root string) ([]File, Stats, error) {
	stats := Stats{Excluded: make(map[string]int, len(exclusionRules))}
	for _, name := range RuleNames() {
		stats.Excluded[name] = 0
	}

	var files []File
	err := filepath.WalkDir(root, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !strings.HasSuffix(rel, ".go") {
			return nil
		}
		if rule, excluded := excludedRel(rel, abs); excluded {
			stats.Excluded[rule]++
			return nil
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return err
		}
		f := File{
			Path:    rel,
			Package: ImportPath(rel),
			SHA:     sha256Hex(data),
			LOC:     countLOC(data),
			InSlice: InSlice(rel),
		}
		files = append(files, f)
		stats.Indexed++
		stats.LOC += f.LOC
		if f.InSlice {
			stats.InSlice++
			stats.InSliceLOC += f.LOC
		}
		return nil
	})
	if err != nil {
		return nil, stats, err
	}
	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return files, stats, nil
}

// ImportPath returns the Go import path for a repo-root-relative slash path.
// The root module is k8s.io/kubernetes; a path under staging/src/ belongs to
// the staging module rooted there, which is how go.work maps staging modules
// (for example staging/src/k8s.io/client-go/tools/cache maps to
// k8s.io/client-go/tools/cache).
func ImportPath(relPath string) string {
	dir := path.Dir(relPath)
	if dir == "." {
		dir = ""
	}
	if rest, ok := strings.CutPrefix(dir, "staging/src/"); ok {
		return rest
	}
	return path.Join("k8s.io/kubernetes", dir)
}

// countLOC counts non-blank lines.
func countLOC(data []byte) int {
	n := 0
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			n++
		}
	}
	return n
}

// sha256Hex is the lowercase hex sha256 of data.
func sha256Hex(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
