package walk

import (
	"bufio"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// BuildContext returns the build context every corpus consumer filters with:
// linux/amd64 with cgo off, constructed explicitly so an inherited
// CGO_ENABLED never changes the corpus (D-058, D-063). A Go file that imports
// "C" is excluded under build_constraint even when the shell enables cgo.
func BuildContext() *build.Context {
	return &build.Context{GOOS: "linux", GOARCH: "amd64", CgoEnabled: false}
}

// exclusionRule is one named predicate from architecture.md#corpus-and-slice.
// A predicate returns true when the repo-relative slash path rel or the
// on-disk file abs is rejected.
type exclusionRule struct {
	name  string
	match func(rel, abs string) bool
}

// exclusionRules is the ordered table of exclusion rules. The order decides
// which rule is credited when several match, and follows the sprint list.
var exclusionRules = []exclusionRule{
	{"vendor", func(rel, _ string) bool { return strings.HasPrefix(rel, "vendor/") }},
	{"zz_generated", func(_, abs string) bool { return strings.HasPrefix(filepath.Base(abs), "zz_generated") }},
	{"pb_go", func(_, abs string) bool { return strings.HasSuffix(filepath.Base(abs), ".pb.go") }},
	{"testdata", func(rel, _ string) bool { return hasSegment(rel, "testdata") }},
	{"test_go", func(_, abs string) bool { return strings.HasSuffix(filepath.Base(abs), "_test.go") }},
	{"client_go_generated", clientGoGenerated},
	{"build_constraint", buildConstraint},
}

// clientGoGeneratedPaths are the generated client-go trees that are never
// indexed even without a generated header.
var clientGoGeneratedPaths = []string{
	"staging/src/k8s.io/client-go/kubernetes",
	"staging/src/k8s.io/client-go/listers",
	"staging/src/k8s.io/client-go/informers",
	"staging/src/k8s.io/client-go/applyconfigurations",
}

// clientGoGenerated rejects generated client-go trees and any Go file whose
// header carries both generator markers.
func clientGoGenerated(rel, abs string) bool {
	return hasAnyPrefix(rel, clientGoGeneratedPaths) || generatedHeader(abs)
}

// generatedHeader reports whether abs carries both "Code generated" and
// "DO NOT EDIT" within its first ten lines.
func generatedHeader(abs string) bool {
	f, err := os.Open(abs)
	if err != nil {
		return false
	}
	defer f.Close()

	generated, doNotEdit := false, false
	sc := bufio.NewScanner(f)
	for lines := 0; lines < 10 && sc.Scan(); lines++ {
		line := sc.Text()
		if strings.Contains(line, "Code generated") {
			generated = true
		}
		if strings.Contains(line, "DO NOT EDIT") {
			doNotEdit = true
		}
	}
	return generated && doNotEdit
}

// buildConstraint rejects a file the build context would not load: a name or
// //go:build tag selecting another OS/arch, or a cgo file while cgo is off.
func buildConstraint(_ string, abs string) bool {
	ctxt := BuildContext()
	match, err := ctxt.MatchFile(filepath.Dir(abs), filepath.Base(abs))
	if err != nil || !match {
		return true
	}
	return !ctxt.CgoEnabled && importsC(abs)
}

// importsC reports whether abs imports "C".
func importsC(abs string) bool {
	f, err := parser.ParseFile(token.NewFileSet(), abs, nil, parser.ImportsOnly)
	if err != nil {
		return false
	}
	for _, imp := range f.Imports {
		if imp.Path.Value == `"C"` {
			return true
		}
	}
	return false
}

// Excluded reports whether the candidate file at p is excluded from the index
// and which named rule rejected it. root is the repository (or fixture) root;
// p may be absolute or root-relative (the mining probe in S1-12 works with
// repo-relative GitHub paths). Only .go files can be excluded, so a non-Go
// file returns ("", false) and the caller must not index it. The rule table is
// defined once here and shared by walk, callgraph-gt (S1-08) and the mining
// probe (S1-12); nothing re-implements it.
func Excluded(root, p string) (rule string, excluded bool) {
	rel, abs := p, p
	if filepath.IsAbs(p) {
		r, err := filepath.Rel(root, p)
		if err != nil {
			return "", false
		}
		rel = r
	} else {
		abs = filepath.Join(root, p)
	}
	return excludedRel(filepath.ToSlash(rel), abs)
}

// excludedRel is the rule loop over an already-computed repo-relative path.
func excludedRel(rel, abs string) (string, bool) {
	if !strings.HasSuffix(rel, ".go") {
		return "", false
	}
	for _, r := range exclusionRules {
		if r.match(rel, abs) {
			return r.name, true
		}
	}
	return "", false
}

// RuleNames returns the exclusion rule names in evaluation order.
func RuleNames() []string {
	names := make([]string, len(exclusionRules))
	for i, r := range exclusionRules {
		names[i] = r.name
	}
	return names
}

// hasSegment reports whether seg is a whole path segment of the slash path p.
func hasSegment(p, seg string) bool {
	for _, s := range strings.Split(p, "/") {
		if s == seg {
			return true
		}
	}
	return false
}
