// Package source reads line ranges of source files from the pinned tree. It is
// the one reader the summary prompt builder, the quality gate and the audit
// share, so a range off-by-one is fixed once.
package source

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReadRange returns lines start..end (1-based, inclusive) of relPath under
// root. start is clamped up to 1 and end down to the line count; an empty range
// returns "". A read error names relPath.
func ReadRange(root, relPath string, start, end int) (string, error) {
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", relPath, err)
	}
	return Range(strings.Split(string(src), "\n"), start, end), nil
}

// Range returns lines start..end (1-based, inclusive) joined by newlines, with
// the same clamping as [ReadRange].
func Range(lines []string, start, end int) string {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return ""
	}
	return strings.Join(lines[start-1:end], "\n")
}
