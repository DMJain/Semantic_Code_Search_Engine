package ci

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ticketID matches sprint-ticket and close-out-ticket identifiers (a sprint
// number and a ticket number joined by a dash, the same with a T marker, the
// close-out form, or "sprint" followed by a T number). Decision ids are not
// matched: they are decisions, not tickets.
var ticketID = regexp.MustCompile(`\bS\d+-T\d+|\bsprint T\d+|\bS\d+C-\d+|\bS\d+-\d\d\b`)

// TestNoTicketIDsInSource fails when a ticket identifier appears in source,
// scripts, SQL, the Makefile or workflows. Ticket ids belong in docs/ and
// .scratch/, where the plan lives; code and comments must read without them.
func TestNoTicketIDsInSource(t *testing.T) {
	root := repoRoot(t)
	skipDirs := map[string]bool{
		".git": true, ".scratch": true, "docs": true, "data": true,
		"bin": true, "node_modules": true,
	}
	self := "tickets_test.go"
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		switch ext := filepath.Ext(name); {
		case name == self:
			return nil
		case name == "Makefile", ext == ".go", ext == ".sql", ext == ".sh",
			ext == ".yml", ext == ".yaml", ext == ".json":
		default:
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		for i, line := range strings.Split(string(b), "\n") {
			if m := ticketID.FindString(line); m != "" {
				t.Errorf("%s:%d: ticket identifier %q; remove it and reword the comment", rel, i+1, m)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
