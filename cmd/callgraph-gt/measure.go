package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/callgraph/vta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
)

// measureLoadMode is the full load needed to build SSA for the slice roots:
// names, files, syntax, type information, imports and dependencies. The same
// mode is reused when the ground-truth graph is stored.
const measureLoadMode = packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
	packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps

// defaultMeasureOut is where the JSON result metadata is written when --out is
// not given.
const defaultMeasureOut = "eval/results/callgraph/gt-memory.json"

// offlineResult records the GOPROXY=off GOFLAGS=-mod=readonly load attempt
// (Q-05). Error is the exact failure text when the attempt did not succeed.
type offlineResult struct {
	Attempted bool   `json:"attempted"`
	Succeeded bool   `json:"succeeded"`
	Error     string `json:"error"`
}

// measureResult is the stable shape of the read-only measurement metadata.
// peak_rss_bytes is the process peak resident set after the main load.
type measureResult struct {
	PeakRSSBytes   int64         `json:"peak_rss_bytes"`
	WallMS         int64         `json:"wall_ms"`
	PackagesLoaded int           `json:"packages_loaded"`
	SSAFunctions   int           `json:"ssa_functions"`
	VTAEdges       int           `json:"vta_edges"`
	InSliceEdges   int           `json:"in_slice_edges"`
	LoadInvocation string        `json:"load_invocation"`
	OfflineLoad    offlineResult `json:"offline_load"`
	GOMEMLIMIT     string        `json:"gomemlimit"`
}

// runMeasure implements --measure: a read-only run that loads the slice,
// builds SSA and the VTA call graph and writes the measurement JSON. It never
// touches the database.
func runMeasure(out string) int {
	root, err := filepath.Abs(config.LoadEnv().KubeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "callgraph-gt --measure: %v\n", err)
		return 2
	}
	res, err := measureSlice(root, out, os.Environ())
	if err != nil {
		fmt.Fprintf(os.Stderr, "callgraph-gt --measure: %v\n", err)
		return 2
	}
	fmt.Printf("peak_rss_bytes: %d\n", res.PeakRSSBytes)
	fmt.Printf("wall_ms:        %d\n", res.WallMS)
	fmt.Printf("packages:       %d\n", res.PackagesLoaded)
	fmt.Printf("ssa_functions:  %d\n", res.SSAFunctions)
	fmt.Printf("vta_edges:      %d\n", res.VTAEdges)
	fmt.Printf("in_slice_edges: %d\n", res.InSliceEdges)
	fmt.Printf("load:           %s\n", res.LoadInvocation)
	if res.OfflineLoad.Succeeded {
		fmt.Println("offline load:   succeeded")
	} else {
		fmt.Printf("offline load:   failed: %s\n", res.OfflineLoad.Error)
	}
	return 0
}

// sliceGraph is the shared SSA/VTA build over the slice: the program, the VTA
// call graph and the in-slice file and slice-package sets. --measure and the
// ground-truth store share the load/build code but not the root policy, so
// --measure keeps the original minimal VTA input while the store can see the
// synthetic callers story 17 requires.
type sliceGraph struct {
	root       string
	prog       *ssa.Program
	graph      *callgraph.Graph
	sliceFiles map[string]bool
	slicePkgs  map[string]bool
	packages   int
	ssaFuncs   int
}

// rootPolicy chooses the VTA seed set.
type rootPolicy int

const (
	// rootsSliceOnly seeds VTA with the slice functions only (the memory
	// measurement's minimal input, story 10).
	rootsSliceOnly rootPolicy = iota
	// rootsSliceAndSynthetic also seeds the position-less synthetic functions of
	// the slice packages, so package-variable initialiser callers are visible
	// and can be dropped and counted by the store (story 17).
	rootsSliceAndSynthetic
)

// buildSliceGraph loads the slice only, builds SSA with generics instantiated
// and computes the VTA graph seeded by sliceRoots over the CHA graph. It is the
// single load/build path shared by --measure and the ground-truth store; policy
// selects the seed set.
func buildSliceGraph(root string, environ []string, policy rootPolicy) (*sliceGraph, error) {
	cfg := &packages.Config{
		Mode:  measureLoadMode,
		Dir:   root,
		Env:   buildEnv(environ, workspaceMode.over...),
		Tests: false,
	}
	pkgs, err := packages.Load(cfg, slicePatterns()...)
	if err != nil {
		return nil, err
	}
	if errs := collectLoadErrors(pkgs); len(errs) > 0 {
		return nil, fmt.Errorf("slice load errors: %s", strings.Join(errs, "; "))
	}

	prog, _ := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	prog.Build()

	sliceFiles := inSliceFileSet(root, pkgs)
	slicePkgs := make(map[string]bool, len(pkgs))
	for _, p := range pkgs {
		slicePkgs[p.PkgPath] = true
	}
	funcs := ssautil.AllFunctions(prog)
	graph := vta.CallGraph(sliceRoots(prog, funcs, sliceFiles, slicePkgs, policy), cha.CallGraph(prog))

	return &sliceGraph{
		root:       root,
		prog:       prog,
		graph:      graph,
		sliceFiles: sliceFiles,
		slicePkgs:  slicePkgs,
		packages:   len(pkgs),
		ssaFuncs:   len(funcs),
	}, nil
}

// sliceRoots is the VTA input. Under rootsSliceOnly it is every in-slice
// function (the memory measurement). Under rootsSliceAndSynthetic it also
// includes the position-less synthetic functions of the slice packages, so
// package-variable initialiser callers are visible and can be dropped and
// counted (story 17).
func sliceRoots(prog *ssa.Program, funcs map[*ssa.Function]bool, sliceFiles, slicePkgs map[string]bool, policy rootPolicy) map[*ssa.Function]bool {
	roots := make(map[*ssa.Function]bool)
	for fn := range funcs {
		if fnInSlice(prog, fn, sliceFiles) || (policy == rootsSliceAndSynthetic && syntheticSliceFn(fn, slicePkgs)) {
			roots[fn] = true
		}
	}
	return roots
}

// syntheticSliceFn reports whether fn is a position-less, synthetic function of
// a loaded slice package (the package-variable initialiser).
func syntheticSliceFn(fn *ssa.Function, slicePkgs map[string]bool) bool {
	return fn != nil && fn.Pos() == 0 && fn.Pkg != nil && fn.Pkg.Pkg != nil && slicePkgs[fn.Pkg.Pkg.Path()]
}

// measureSlice is the whole read-only measurement, factored out so a test can
// run it over the fixture module. It loads the slice only, builds SSA, seeds
// VTA with the slice functions over the CHA graph, counts and (when out is
// non-empty) writes the metadata JSON.
func measureSlice(root, out string, environ []string) (measureResult, error) {
	res := measureResult{GOMEMLIMIT: envValue(environ, "GOMEMLIMIT")}

	// The offline attempt runs first with a metadata-only load: it is small, so
	// it cannot inflate the peak RSS measured after the full load, and it
	// cannot be starved if the full load is near the memory limit (Q-05).
	res.OfflineLoad = attemptOfflineLoad(root, environ)

	start := time.Now()

	sg, err := buildSliceGraph(root, environ, rootsSliceOnly)
	if err != nil {
		return res, err
	}
	res.PackagesLoaded = sg.packages
	res.SSAFunctions = sg.ssaFuncs
	res.VTAEdges, res.InSliceEdges = countEdges(sg.prog, sg.graph, sg.sliceFiles)

	res.WallMS = time.Since(start).Milliseconds()
	res.PeakRSSBytes = peakRSS()
	res.LoadInvocation = measureInvocation()

	if out != "" {
		if err := writeMeasureResult(out, res); err != nil {
			return res, err
		}
	}
	return res, nil
}

// attemptOfflineLoad runs the slice load with GOPROXY=off and
// GOFLAGS=-mod=readonly and records whether it resolved. A metadata-only load
// (need name, files, imports, deps) is the `go list -deps` equivalent D-025
// names; it proves the module layout resolves offline without paying for the
// full type-check.
func attemptOfflineLoad(root string, environ []string) offlineResult {
	res := offlineResult{Attempted: true}
	over := append(append([]string{}, workspaceMode.over...), "GOPROXY=off", "GOFLAGS=-mod=readonly")
	cfg := &packages.Config{
		Mode:  packages.NeedName | packages.NeedFiles | packages.NeedImports | packages.NeedDeps,
		Dir:   root,
		Env:   buildEnv(environ, over...),
		Tests: false,
	}
	pkgs, err := packages.Load(cfg, slicePatterns()...)
	switch {
	case err != nil:
		res.Error = err.Error()
	case len(pkgs) == 0:
		res.Error = "loaded no packages"
	default:
		if errs := collectLoadErrors(pkgs); len(errs) > 0 {
			res.Error = strings.Join(errs, "; ")
		} else {
			res.Succeeded = true
		}
	}
	return res
}

// collectLoadErrors returns every load error text reachable from the initial
// packages. It follows the import graph, so an error on a dependency package
// (not just on the initial packages) is seen; with NeedDeps that is where
// module-resolution failures live.
func collectLoadErrors(pkgs []*packages.Package) []string {
	var errs []string
	packages.Visit(pkgs, func(p *packages.Package) bool {
		for _, e := range p.Errors {
			errs = append(errs, e.Error())
		}
		return true
	}, nil)
	return errs
}

// inSliceFileSet returns the absolute paths of the loaded files that are in the
// frozen slice. The mapping is the shared inSliceRel helper, so it cannot drift
// from --check. Both the raw and the resolved path are keyed so SSA positions
// match whichever form go/packages reported.
func inSliceFileSet(root string, pkgs []*packages.Package) map[string]bool {
	set := map[string]bool{}
	for _, p := range pkgs {
		for _, abs := range append(append([]string{}, p.GoFiles...), p.CompiledGoFiles...) {
			if !strings.HasSuffix(abs, ".go") {
				continue
			}
			if _, _, included := inSliceRel(root, abs); !included {
				continue
			}
			set[abs] = true
			if r, err := filepath.EvalSymlinks(abs); err == nil {
				set[r] = true
			}
		}
	}
	return set
}

// fnInSlice reports whether the function's source file is in the slice set.
func fnInSlice(prog *ssa.Program, fn *ssa.Function, set map[string]bool) bool {
	if fn == nil || fn.Pos() == 0 {
		return false
	}
	name := prog.Fset.Position(fn.Pos()).Filename
	return name != "" && set[name]
}

// countEdges returns the total VTA edge count and the count with both caller
// and callee in slice files.
func countEdges(prog *ssa.Program, graph *callgraph.Graph, set map[string]bool) (total, inSlice int) {
	for _, n := range graph.Nodes {
		for _, e := range n.Out {
			total++
			if e.Caller != nil && e.Callee != nil &&
				fnInSlice(prog, e.Caller.Func, set) && fnInSlice(prog, e.Callee.Func, set) {
				inSlice++
			}
		}
	}
	return total, inSlice
}

// measureInvocation is the recorded working invocation: the workspace build
// context plus the slice patterns (D-025, refs D-071).
func measureInvocation() string {
	parts := append(append([]string{}, workspaceMode.over...), "CGO_ENABLED=0")
	return strings.Join(parts, " ") + " go list -deps " + strings.Join(slicePatterns(), " ")
}

// envValue returns the last value for key in an "KEY=VALUE" environment slice,
// or "" when the key is absent.
func envValue(environ []string, key string) string {
	prefix := key + "="
	for i := len(environ) - 1; i >= 0; i-- {
		if strings.HasPrefix(environ[i], prefix) {
			return strings.TrimPrefix(environ[i], prefix)
		}
	}
	return ""
}

// peakRSS reads the process peak resident set size. getrusage reports bytes on
// darwin and kibibytes on linux, so the value is normalised to bytes.
func peakRSS() int64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	v := ru.Maxrss
	if runtime.GOOS == "linux" {
		return v * 1024
	}
	return v
}

// writeMeasureResult writes the metadata JSON to path, creating its directory.
func writeMeasureResult(path string, res measureResult) error {
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, b, 0o644)
}
