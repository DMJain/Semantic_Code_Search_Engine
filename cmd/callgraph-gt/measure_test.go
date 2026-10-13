package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// TestMeasureEnvIsTheCorpusBuildContext proves the environment handed to the
// measurement load is the workspace invocation the corpus build context fixes
// (D-058, D-071): GOOS=linux, GOARCH=amd64, GOWORK=auto and GOTOOLCHAIN=local,
// with CGO_ENABLED=0 last so an inherited cgo setting can never win.
func TestMeasureEnvIsTheCorpusBuildContext(t *testing.T) {
	t.Setenv("CGO_ENABLED", "1")

	env := buildEnv(os.Environ(), workspaceMode.over...)
	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	for _, want := range []string{"GOOS=linux", "GOARCH=amd64", "GOWORK=auto", "GOTOOLCHAIN=local", "CGO_ENABLED=0"} {
		k, v, _ := strings.Cut(want, "=")
		if got[k] != v {
			t.Errorf("env[%s] = %q, want %q", k, got[k], v)
		}
	}
	if env[len(env)-1] != "CGO_ENABLED=0" {
		t.Errorf("last env entry = %q, want CGO_ENABLED=0 last", env[len(env)-1])
	}
}

// TestMeasureResultJSONKeys pins the result metadata shape: an external
// consumer (and the acceptance criteria) reads these exact keys.
func TestMeasureResultJSONKeys(t *testing.T) {
	b, err := json.Marshal(measureResult{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []string{
		"peak_rss_bytes", "wall_ms", "packages_loaded", "ssa_functions",
		"vta_edges", "in_slice_edges", "load_invocation", "offline_load", "gomemlimit",
	}
	if len(top) != len(want) {
		t.Errorf("result has %d keys, want %d: %v", len(top), len(want), top)
	}
	for _, k := range want {
		if _, ok := top[k]; !ok {
			t.Errorf("result is missing key %q", k)
		}
	}

	var off map[string]json.RawMessage
	if err := json.Unmarshal(top["offline_load"], &off); err != nil {
		t.Fatalf("offline_load is not an object: %v", err)
	}
	for _, k := range []string{"attempted", "succeeded", "error"} {
		if _, ok := off[k]; !ok {
			t.Errorf("offline_load is missing key %q", k)
		}
	}
}

// TestCollectLoadErrorsWalksImportGraph proves a load error on a dependency
// package is collected, not only on the initial packages: with NeedDeps the
// module-resolution failures live on dependency packages reachable via Imports.
func TestCollectLoadErrorsWalksImportGraph(t *testing.T) {
	dep := &packages.Package{
		PkgPath: "example.com/dep",
		Errors:  []packages.Error{{Pos: "dep.go:1:1", Msg: "module lookup disabled by GOPROXY=off"}},
	}
	root := &packages.Package{
		PkgPath: "example.com/root",
		Imports: map[string]*packages.Package{"example.com/dep": dep},
	}

	errs := collectLoadErrors([]*packages.Package{root})
	if len(errs) == 0 {
		t.Fatal("dependency error was not collected")
	}
	if joined := strings.Join(errs, "; "); !strings.Contains(joined, "module lookup disabled by GOPROXY=off") {
		t.Errorf("dependency error missing from %q", joined)
	}
}

// TestMeasureBuildsSSAAndCounts runs the read-only measurement pipeline over
// the committed fixture module: it must load, build SSA, compute a VTA graph
// and report counts without touching a database.
func TestMeasureBuildsSSAAndCounts(t *testing.T) {
	root, err := filepath.Abs("testdata/module")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	res, err := measureSlice(root, "", os.Environ())
	if err != nil {
		t.Fatalf("measureSlice: %v", err)
	}
	if res.PackagesLoaded <= 0 {
		t.Errorf("packages_loaded = %d, want > 0", res.PackagesLoaded)
	}
	if res.SSAFunctions <= 0 {
		t.Errorf("ssa_functions = %d, want > 0", res.SSAFunctions)
	}
	if res.VTAEdges <= 0 {
		t.Errorf("vta_edges = %d, want > 0 (the fixture has real calls)", res.VTAEdges)
	}
	if res.InSliceEdges <= 0 {
		t.Errorf("in_slice_edges = %d, want > 0 (the calls are inside the slice)", res.InSliceEdges)
	}
	if res.WallMS < 0 {
		t.Errorf("wall_ms = %d, want >= 0", res.WallMS)
	}
	if res.PeakRSSBytes <= 0 {
		t.Errorf("peak_rss_bytes = %d, want > 0", res.PeakRSSBytes)
	}
	if res.LoadInvocation == "" {
		t.Error("load_invocation is empty")
	}
	if !strings.Contains(res.LoadInvocation, "./pkg/scheduler/...") {
		t.Errorf("load_invocation %q does not name the slice roots", res.LoadInvocation)
	}
	if !res.OfflineLoad.Attempted {
		t.Error("offline_load was not attempted")
	}
}
