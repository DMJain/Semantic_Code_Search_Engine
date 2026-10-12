package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/walk"
)

// slicePatterns returns the go/packages patterns for the frozen slice's roots
// only (D-067, D-070): every walk.SlicePrefixes root that the drop list does
// not remove whole. Roots are addressed relative to the clone root, so a
// dropped root such as client-go is never loaded.
func slicePatterns() []string {
	var patterns []string
	for _, root := range walk.SliceRoots() {
		patterns = append(patterns, "./"+root+"/...")
	}
	return patterns
}

// maxDiffLines caps each difference list printed on exit 1.
const maxDiffLines = 50

// loadMode is one environment under which the slice roots are loaded (Q-05).
type loadMode struct {
	name   string
	vendor bool
	over   []string
}

var (
	// pinnedMode is the invocation D-058/D-067 assume.
	pinnedMode = loadMode{
		name:   "pinned (GOWORK=off, -mod=vendor)",
		vendor: true,
		over:   []string{"GOOS=linux", "GOARCH=amd64", "GOWORK=off", "GOTOOLCHAIN=local"},
	}
	// workspaceMode is the fallback Q-05 requires: default module resolution
	// (GOWORK=auto), no forced -mod=vendor.
	workspaceMode = loadMode{
		name: "workspace (GOWORK=auto, default module resolution)",
		over: []string{"GOOS=linux", "GOARCH=amd64", "GOWORK=auto", "GOTOOLCHAIN=local"},
	}
)

// buildEnv returns environ followed by the explicit corpus-build entries, with
// CGO_ENABLED=0 appended last so an inherited CGO_ENABLED can never win
// (D-058, D-063). The returned slice always ends with "CGO_ENABLED=0".
func buildEnv(environ []string, overrides ...string) []string {
	env := make([]string, 0, len(environ)+len(overrides)+1)
	env = append(env, environ...)
	env = append(env, overrides...)
	env = append(env, "CGO_ENABLED=0")
	return env
}

// loadResult is one go/packages load: the kept repo-relative slice files, the
// per-rule exclusion counts and every package load error text.
type loadResult struct {
	kept     []string
	excluded map[string]int
	errs     []string
}

// ok reports whether the load made progress: it completed without package
// errors and produced at least one in-slice file.
func (r loadResult) ok() bool { return len(r.errs) == 0 && len(r.kept) > 0 }

// loadSlice loads patterns from root under mode. Every loaded file is mapped to
// a repo-root-relative slash path with filepath.EvalSymlinks applied, filtered
// with the shared walk.Excluded predicate, and kept only when it is in the
// slice. Load errors from any returned package are collected, not swallowed.
func loadSlice(root string, patterns []string, mode loadMode, environ []string) (loadResult, error) {
	res := loadResult{excluded: map[string]int{}}
	cfg := &packages.Config{
		Mode:  packages.NeedName | packages.NeedFiles,
		Dir:   root,
		Env:   buildEnv(environ, mode.over...),
		Tests: false,
	}
	if mode.vendor {
		cfg.BuildFlags = []string{"-mod=vendor"}
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return res, err
	}

	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	seen := map[string]bool{}
	for _, p := range pkgs {
		for _, e := range p.Errors {
			res.errs = append(res.errs, e.Error())
		}
		for _, abs := range p.GoFiles {
			if resolved, err := filepath.EvalSymlinks(abs); err == nil {
				abs = resolved
			}
			rel, err := filepath.Rel(root, abs)
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if !walk.InSlice(rel) {
				continue
			}
			if rule, excluded := walk.Excluded(root, abs); excluded {
				res.excluded[rule]++
				continue
			}
			if !seen[rel] {
				seen[rel] = true
				res.kept = append(res.kept, rel)
			}
		}
	}
	sort.Strings(res.kept)
	return res, nil
}

// loadSliceWithFallback tries the pinned invocation first and, when it cannot
// make progress, the workspace Q-05 requires. It returns the result and the
// human-readable environment that loaded it; when both fail the error carries
// both exact failures for the Q-05 note.
func loadSliceWithFallback(root string, patterns []string, environ []string) (loadResult, string, error) {
	pinned, pinnedErr := loadSlice(root, patterns, pinnedMode, environ)
	if pinnedErr == nil && pinned.ok() {
		return pinned, pinnedMode.name, nil
	}
	workspace, workspaceErr := loadSlice(root, patterns, workspaceMode, environ)
	if workspaceErr == nil && workspace.ok() {
		return workspace, workspaceMode.name, nil
	}
	return loadResult{}, "", fmt.Errorf("no environment could load the slice roots (Q-05):\n  %s: %s\n  %s: %s",
		pinnedMode.name, describeFailure(pinned, pinnedErr),
		workspaceMode.name, describeFailure(workspace, workspaceErr))
}

// describeFailure renders why one load attempt failed.
func describeFailure(r loadResult, err error) string {
	switch {
	case err != nil:
		return err.Error()
	case len(r.errs) > 0:
		return strings.Join(r.errs, "; ")
	default:
		return "loaded no slice files"
	}
}

// sliceLoader loads the type-checker file set for the slice roots under root
// and names the environment that loaded it.
type sliceLoader func(root string) (loadResult, string, error)

// realLoader loads the frozen slice roots with the pinned invocation and the
// workspace fallback.
func realLoader(root string) (loadResult, string, error) {
	return loadSliceWithFallback(root, slicePatterns(), os.Environ())
}

// runCheck implements --check. It needs no database: the walk side is a fresh
// walk of the pinned tree, so a stale files table cannot make it pass.
func runCheck() int {
	cfg := config.LoadEnv()
	root, err := filepath.Abs(cfg.KubeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "callgraph-gt --check: %v\n", err)
		return 2
	}
	printSymlinkFact(root)
	return checkSlice(root, realLoader, os.Stdout, os.Stderr)
}

// checkSlice walks root afresh (same exclusion rules and corpus build
// context), compares the in-slice files with the loader's set, prints the
// exclusion counts and the symmetric difference, and returns 0 (equal), 1
// (unequal) or 2 (cannot check).
func checkSlice(root string, load sliceLoader, out, errOut io.Writer) int {
	files, stats, err := walk.Walk(root)
	if err != nil {
		fmt.Fprintf(errOut, "callgraph-gt --check: %v\n", err)
		return 2
	}
	var walkPaths []string
	for _, f := range files {
		if f.InSlice {
			walkPaths = append(walkPaths, f.Path)
		}
	}

	res, envName, err := load(root)
	if err != nil {
		fmt.Fprintf(errOut, "callgraph-gt --check: %v\n", err)
		return 2
	}
	fmt.Fprintf(out, "load: %s\n", envName)
	fmt.Fprintf(out, "walk excluded:     %s\n", formatCounts(stats.Excluded))
	fmt.Fprintf(out, "packages excluded: %s\n", formatCounts(res.excluded))

	walkSet := stringSet(walkPaths)
	pkgSet := stringSet(res.kept)
	onlyWalk, onlyPackages := diffSets(walkSet, pkgSet)
	if len(onlyWalk) == 0 && len(onlyPackages) == 0 {
		fmt.Fprintf(out, "file-set: equal (%d files)\n", len(walkSet))
		return 0
	}
	fmt.Fprintf(out, "file-set: unequal (walk %d, packages %d)\n", len(walkSet), len(pkgSet))
	printDiff(out, "only in walk", onlyWalk)
	printDiff(out, "only in packages", onlyPackages)
	return 1
}

// printSymlinkFact records whether vendor/k8s.io/client-go is a symlink at the
// pinned tag (Q-05).
func printSymlinkFact(root string) {
	p := filepath.Join(root, "vendor", "k8s.io", "client-go")
	fi, err := os.Lstat(p)
	switch {
	case errors.Is(err, os.ErrNotExist):
		fmt.Println("vendor/k8s.io/client-go: absent (no symlink, no directory; Q-05)")
	case err != nil:
		fmt.Printf("vendor/k8s.io/client-go: cannot stat: %v\n", err)
	case fi.Mode()&os.ModeSymlink != 0:
		target, _ := os.Readlink(p)
		fmt.Printf("vendor/k8s.io/client-go: symlink -> %s\n", target)
	case fi.IsDir():
		fmt.Println("vendor/k8s.io/client-go: directory (not a symlink)")
	default:
		fmt.Println("vendor/k8s.io/client-go: present, not a symlink")
	}
}

// formatCounts renders the per-rule exclusion counts in evaluation order, so
// both sides print the same rules.
func formatCounts(counts map[string]int) string {
	parts := make([]string, 0, len(walk.RuleNames()))
	for _, name := range walk.RuleNames() {
		parts = append(parts, fmt.Sprintf("%s=%d", name, counts[name]))
	}
	return strings.Join(parts, " ")
}

// stringSet turns a slice of paths into a set.
func stringSet(paths []string) map[string]bool {
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[p] = true
	}
	return set
}

// diffSets returns the paths only in a and only in b, each sorted.
func diffSets(a, b map[string]bool) (onlyA, onlyB []string) {
	for p := range a {
		if !b[p] {
			onlyA = append(onlyA, p)
		}
	}
	for p := range b {
		if !a[p] {
			onlyB = append(onlyB, p)
		}
	}
	sort.Strings(onlyA)
	sort.Strings(onlyB)
	return onlyA, onlyB
}

// printDiff prints a difference list with its total and a cap of maxDiffLines.
func printDiff(out io.Writer, label string, paths []string) {
	fmt.Fprintf(out, "%s (%d):\n", label, len(paths))
	for i, p := range paths {
		if i >= maxDiffLines {
			fmt.Fprintf(out, "  ... (%d more)\n", len(paths)-maxDiffLines)
			break
		}
		fmt.Fprintf(out, "  %s\n", p)
	}
}
