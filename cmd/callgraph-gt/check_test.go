package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/walk"
)

// TestBuildEnvEndsWithCgoDisabled proves the environment handed to go/packages
// ends with CGO_ENABLED=0 whatever the process environment holds (D-063): an
// inherited CGO_ENABLED=1 is never allowed to win.
func TestBuildEnvEndsWithCgoDisabled(t *testing.T) {
	t.Setenv("CGO_ENABLED", "1")

	for _, mode := range []loadMode{pinnedMode, workspaceMode} {
		env := buildEnv(os.Environ(), mode.over...)
		if got := env[len(env)-1]; got != "CGO_ENABLED=0" {
			t.Errorf("%s: last env entry = %q, want %q", mode.name, got, "CGO_ENABLED=0")
		}
	}

	env := buildEnv([]string{"CGO_ENABLED=1", "HOME=/tmp"}, "GOOS=linux")
	if got := env[len(env)-1]; got != "CGO_ENABLED=0" {
		t.Errorf("last env entry = %q, want %q", got, "CGO_ENABLED=0")
	}
	if env[0] != "CGO_ENABLED=1" {
		t.Errorf("inherited environment not preserved first: %v", env)
	}
}

// TestFixtureWalkEqualsPackages proves, on the committed fixture module with a
// _windows.go file, a //go:build windows file and an import "C" file, that the
// walk set equals the go/packages set under the identical build context while
// CGO_ENABLED=1 is in the process environment.
func TestFixtureWalkEqualsPackages(t *testing.T) {
	t.Setenv("CGO_ENABLED", "1")

	root, err := filepath.Abs("testdata/module")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	files, stats, err := walk.Walk(root)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	walkSet := make([]string, len(files))
	for i, f := range files {
		walkSet[i] = f.Path
	}

	fixtureMode := loadMode{
		name: "fixture (GOWORK=off, no vendor)",
		over: []string{"GOOS=linux", "GOARCH=amd64", "GOWORK=off", "GOTOOLCHAIN=local"},
	}
	res, err := loadSlice(root, []string{"./pkg/scheduler/..."}, fixtureMode, os.Environ())
	if err != nil {
		t.Fatalf("loadSlice: %v", err)
	}
	if len(res.errs) > 0 {
		t.Fatalf("load errors: %v", res.errs)
	}

	if !slices.Equal(walkSet, res.kept) {
		t.Errorf("walk set = %v, packages set = %v", walkSet, res.kept)
	}

	if got := stats.Excluded["build_constraint"]; got != 3 {
		t.Errorf("walk build_constraint excluded = %d, want 3 (_windows.go, //go:build windows, import \"C\")", got)
	}
	if got := res.excluded["client_go_generated"]; got != 1 {
		t.Errorf("packages client_go_generated excluded = %d, want 1 (generated header)", got)
	}
}

// fixtureLoader loads the fixture module's scheduler root the way the real
// check loads the slice roots, so the check runs end to end without a clone.
func fixtureLoader(root string) (loadResult, string, error) {
	mode := loadMode{
		name: "fixture",
		over: []string{"GOOS=linux", "GOARCH=amd64", "GOWORK=off", "GOTOOLCHAIN=local"},
	}
	res, err := loadSlice(root, []string{"./pkg/scheduler/..."}, mode, os.Environ())
	return res, mode.name, err
}

// copyFixture copies the fixture module into a temp dir so a test can change
// the tree.
func copyFixture(t *testing.T) string {
	t.Helper()
	src, err := filepath.Abs("testdata/module")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	dst := t.TempDir()
	err = filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	return dst
}

// TestCheckPassesOnEqualFixture: a fresh walk equals the loaded set.
func TestCheckPassesOnEqualFixture(t *testing.T) {
	var out, errOut bytes.Buffer
	code := checkSlice(copyFixture(t), fixtureLoader, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%q err=%q", code, out.String(), errOut.String())
	}
}

// TestCheckWalksTheTreeFreshly: the walk side is computed from the tree at
// check time. A file added after the type-checker side was captured (what a
// stale files table would also miss) makes the check fail.
func TestCheckWalksTheTreeFreshly(t *testing.T) {
	root := copyFixture(t)
	captured, name, err := fixtureLoader(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	added := filepath.Join(root, "pkg", "scheduler", "added.go")
	if err := os.WriteFile(added, []byte("package scheduler\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	stale := func(string) (loadResult, string, error) { return captured, name, nil }

	var out, errOut bytes.Buffer
	if code := checkSlice(root, stale, &out, &errOut); code != 1 {
		t.Fatalf("exit = %d, want 1; out=%q", code, out.String())
	}
	if !strings.Contains(out.String(), "only in walk (1)") || !strings.Contains(out.String(), "pkg/scheduler/added.go") {
		t.Errorf("difference not reported:\n%s", out.String())
	}
}

// TestCheckReportsSeededDifference: a file only the type-checker side holds is
// printed and the exit code is non-zero.
func TestCheckReportsSeededDifference(t *testing.T) {
	root := copyFixture(t)
	seeded := func(r string) (loadResult, string, error) {
		res, name, err := fixtureLoader(r)
		res.kept = append(res.kept, "pkg/scheduler/ghost.go")
		return res, name, err
	}
	var out, errOut bytes.Buffer
	if code := checkSlice(root, seeded, &out, &errOut); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(out.String(), "only in packages (1)") || !strings.Contains(out.String(), "pkg/scheduler/ghost.go") {
		t.Errorf("difference not reported:\n%s", out.String())
	}
}

// TestCheckLoadErrorExitsTwo: a load failure is exit 2, never a pass.
func TestCheckLoadErrorExitsTwo(t *testing.T) {
	failing := func(string) (loadResult, string, error) { return loadResult{}, "", os.ErrNotExist }
	var out, errOut bytes.Buffer
	if code := checkSlice(copyFixture(t), failing, &out, &errOut); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

// TestSlicePatternsLoadOnlyFrozenSliceRoots: the dropped client-go root is
// not among the patterns handed to go/packages.
func TestSlicePatternsLoadOnlyFrozenSliceRoots(t *testing.T) {
	want := []string{"./pkg/scheduler/...", "./pkg/kubelet/...", "./pkg/controller/..."}
	if got := slicePatterns(); !slices.Equal(got, want) {
		t.Errorf("slicePatterns() = %v, want %v", got, want)
	}
}
