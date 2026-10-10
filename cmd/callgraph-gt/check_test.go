package main

import (
	"os"
	"path/filepath"
	"slices"
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
