package summarize

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestStatusAndLevelValues pins the stored spelling of every named status and
// level; the database CHECK constraint (migration 00001) accepts exactly these.
func TestStatusAndLevelValues(t *testing.T) {
	statuses := map[Status]string{StatusPending: "pending", StatusDone: "done", StatusFailed: "failed"}
	for s, want := range statuses {
		if s.String() != want {
			t.Errorf("status %q, want %q", s.String(), want)
		}
	}
	levels := map[Level]string{LevelFunction: "function", LevelFile: "file", LevelPackage: "package"}
	for l, want := range levels {
		if l.String() != want {
			t.Errorf("level %q, want %q", l.String(), want)
		}
	}
}

// TestNoRawStatusOrLevelLiterals fails when a summary status or level is
// written as a string literal (Go string or SQL-quoted) in non-test code
// outside values.go, so a typo cannot silently match nothing.
func TestNoRawStatusOrLevelLiterals(t *testing.T) {
	root := filepath.Join("..", "..")
	// "file" and "package" are everyday words, so only their SQL-quoted form is
	// flagged; the other four are flagged as bare Go strings too.
	exact := []string{"done", "pending", "failed", "function"}
	quoted := []string{"done", "pending", "failed", "function", "file", "package"}
	var offences []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".scratch", "testdata", "node_modules", "data":
				return filepath.SkipDir
			}
			return nil
		}
		slashed := filepath.ToSlash(path)
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
			strings.HasSuffix(slashed, "internal/summarize/values.go") ||
			strings.HasSuffix(slashed, "internal/parse/parse.go") { // tree-sitter field names
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			val := lit.Value
			bare := strings.Trim(val, "\"`")
			hit := slices.Contains(exact, bare)
			for _, w := range quoted {
				if strings.Contains(val, "'"+w+"'") {
					hit = true
				}
			}
			if hit {
				offences = append(offences, fset.Position(lit.Pos()).String()+": "+val)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range offences {
		t.Errorf("raw status/level literal: %s", o)
	}
}
