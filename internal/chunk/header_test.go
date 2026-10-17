package chunk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fullHeader renders the five header lines for the given input at an unlimited
// budget, so a test can measure it before choosing a budget.
func fullHeader(in HeaderInput) string {
	h, _ := buildHeader(in, 1<<30)
	return h
}

// TestBuildHeaderFormat pins the five-line format: package, file, signature,
// calls and called by, in this order, joined with newlines and no trailing
// newline. Empty lists print none and a header that fits is not degraded.
func TestBuildHeaderFormat(t *testing.T) {
	in := HeaderInput{
		Package:   "k8s.io/kubernetes/pkg/scheduler",
		File:      "pkg/scheduler/scheduler.go",
		Signature: "func (s *Scheduler) schedulePod(pod *v1.Pod) error",
		Callees:   []string{"Scheduler.bind", "Scheduler.admit"},
		Callers:   []string{"Scheduler.Run"},
	}
	got, degraded := BuildHeader(in, 100)
	want := strings.Join([]string{
		"// package: k8s.io/kubernetes/pkg/scheduler",
		"// file: pkg/scheduler/scheduler.go",
		"// signature: func (s *Scheduler) schedulePod(pod *v1.Pod) error",
		"// calls: Scheduler.bind, Scheduler.admit",
		"// called by: Scheduler.Run",
	}, "\n")
	if got != want {
		t.Errorf("header mismatch\ngot  %q\nwant %q", got, want)
	}
	if degraded != 0 {
		t.Errorf("header_degraded = %d, want 0", degraded)
	}
	if strings.HasSuffix(got, "\n") {
		t.Errorf("header has a trailing newline: %q", got)
	}

	empty, degraded := BuildHeader(HeaderInput{
		Package:   "k8s.io/kubernetes/extra",
		File:      "extra/extra.go",
		Signature: "func Helper()",
	}, 10)
	want = strings.Join([]string{
		"// package: k8s.io/kubernetes/extra",
		"// file: extra/extra.go",
		"// signature: func Helper()",
		"// calls: none",
		"// called by: none",
	}, "\n")
	if empty != want {
		t.Errorf("empty-list header mismatch\ngot  %q\nwant %q", empty, want)
	}
	if degraded != 0 {
		t.Errorf("empty-list header_degraded = %d, want 0", degraded)
	}
}

// TestShortName renders short qualified names: a method keeps its receiver
// (leading star stripped, package path dropped), a func or type keeps its name.
func TestShortName(t *testing.T) {
	cases := map[string]string{
		"k8s.io/kubernetes/pkg/scheduler.(*Scheduler).schedulePod": "Scheduler.schedulePod",
		"k8s.io/kubernetes/pkg/scheduler.(Scheduler).schedulePod":  "Scheduler.schedulePod",
		"k8s.io/kubernetes/pkg/scheduler.Target":                   "Target",
		"k8s.io/kubernetes/pkg/scheduler.init#1":                   "init#1",
	}
	for in, want := range cases {
		if got := ShortName(in); got != want {
			t.Errorf("ShortName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestTopNamesRanking proves the ranking rule (D-081): exact before name_only,
// ambiguous excluded, then name ascending, with qualified name and id as
// deterministic tie-breaks, capped at five.
func TestTopNamesRanking(t *testing.T) {
	refs := []CallRef{
		{Name: "zeta", QualifiedName: "pkg.zeta", ID: 1, Resolution: ResolutionNameOnly},
		{Name: "Alpha", QualifiedName: "pkg.Alpha", ID: 2, Resolution: ResolutionExact},
		{Name: "beta", QualifiedName: "pkg.beta", ID: 3, Resolution: ResolutionNameOnly},
		{Name: "alpha", QualifiedName: "pkg.alpha", ID: 4, Resolution: ResolutionExact},
		{Name: "bogus", QualifiedName: "pkg.bogus", ID: 5, Resolution: ResolutionAmbiguous},
		{Name: "delta", QualifiedName: "pkg.delta", ID: 6, Resolution: ResolutionNameOnly},
	}
	got := TopNames(refs)
	want := []string{"Alpha", "alpha", "beta", "delta", "zeta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("TopNames = %v, want %v", got, want)
	}

	// Same name: the qualified name and then the id break the tie.
	tie := []CallRef{
		{Name: "m", QualifiedName: "pkg.(*B).m", ID: 9, Resolution: ResolutionExact},
		{Name: "m", QualifiedName: "pkg.(*A).m", ID: 8, Resolution: ResolutionExact},
		{Name: "m", QualifiedName: "pkg.(*A).m", ID: 7, Resolution: ResolutionExact},
	}
	want = []string{"A.m", "A.m", "B.m"}
	if got := TopNames(tie); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("TopNames tie-break = %v, want %v", got, want)
	}
}

// headerGolden is the read-only expectation for the golden header case.
type headerGolden struct {
	Names    []string `json:"names"`
	Header   string   `json:"header"`
	Degraded int      `json:"degraded"`
}

// TestHeaderGolden pins that a symbol with seven callees prints only the top
// five in rank order. Regenerate manually with -update only.
func TestHeaderGolden(t *testing.T) {
	in := HeaderInput{
		Package:   "k8s.io/kubernetes/pkg/extra",
		File:      "pkg/extra/extra.go",
		Signature: "func FanOut()",
	}
	in.Callees = TopNames([]CallRef{
		{Name: "alpha", QualifiedName: "pkg.extra.alpha", ID: 1, Resolution: ResolutionExact},
		{Name: "bravo", QualifiedName: "pkg.extra.bravo", ID: 2, Resolution: ResolutionExact},
		{Name: "charlie", QualifiedName: "pkg.extra.charlie", ID: 3, Resolution: ResolutionExact},
		{Name: "delta", QualifiedName: "pkg.extra.delta", ID: 4, Resolution: ResolutionExact},
		{Name: "echo", QualifiedName: "pkg.extra.echo", ID: 5, Resolution: ResolutionExact},
		{Name: "foxtrot", QualifiedName: "pkg.extra.foxtrot", ID: 6, Resolution: ResolutionExact},
		{Name: "golf", QualifiedName: "pkg.extra.golf", ID: 7, Resolution: ResolutionExact},
	})
	header, degraded := BuildHeader(in, 10)
	got := headerGolden{Names: in.Callees, Header: header, Degraded: degraded}
	data, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden: %v", err)
	}
	data = append(data, '\n')

	path := filepath.Join("testdata", "header.golden")
	if *update {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(data) != string(want) {
		t.Errorf("header golden mismatch (regenerate with -update if intended):\n--- got ---\n%s\n--- want ---\n%s", data, want)
	}
	if len(got.Names) != HeaderListMax {
		t.Errorf("golden callee count = %d, want %d", len(got.Names), HeaderListMax)
	}
}

// TestBuildHeaderBudgetZero proves a budget of zero (content at the embeddable
// ceiling) leaves package and file only and reports degradation.
func TestBuildHeaderBudgetZero(t *testing.T) {
	in := HeaderInput{
		Package:   "k8s.io/kubernetes/pkg/scheduler",
		File:      "pkg/scheduler/scheduler.go",
		Signature: "func (s *Scheduler) schedulePod(pod *v1.Pod) error",
		Callees:   []string{"Scheduler.bind", "Scheduler.admit"},
		Callers:   []string{"Scheduler.Run"},
	}
	got, degraded := BuildHeader(in, EmbeddableContentMax)
	want := "// package: k8s.io/kubernetes/pkg/scheduler\n// file: pkg/scheduler/scheduler.go"
	if got != want {
		t.Errorf("budget-zero header\ngot  %q\nwant %q", got, want)
	}
	if degraded <= 0 {
		t.Errorf("budget-zero header_degraded = %d, want > 0", degraded)
	}
	if degraded != len(in.Callees)+len(in.Callers)+1 {
		t.Errorf("budget-zero header_degraded = %d, want all items plus the signature = %d",
			degraded, len(in.Callees)+len(in.Callers)+1)
	}
}

// TestBuildHeaderDegradation exercises each stage in order, using exact budgets
// derived from the stage's target header so the first fitting header is known.
func TestBuildHeaderDegradation(t *testing.T) {
	in := HeaderInput{
		Package:   "k8s.io/kubernetes/pkg/scheduler",
		File:      "pkg/scheduler/scheduler.go",
		Signature: "func (s *Scheduler) schedulePod(pod *v1.Pod) error",
		Callees:   []string{"Scheduler.bind", "Scheduler.admit"},
		Callers:   []string{"Scheduler.run", "Scheduler.sync", "Scheduler.watch"},
	}
	full := fullHeader(in)
	if len(in.Callers) < 3 || len(in.Callees) < 2 {
		t.Fatalf("fixture needs enough items for every stage")
	}

	// Stage 1: drop the last caller only.
	drop1 := in
	drop1.Callers = in.Callers[:len(in.Callers)-1]
	target := fullHeader(drop1)
	if EstimateTokens(target) >= EstimateTokens(full) {
		t.Fatalf("stage-1 target %d tokens not below full %d", EstimateTokens(target), EstimateTokens(full))
	}
	got, degraded := buildHeader(in, EstimateTokens(target))
	if got != target {
		t.Errorf("stage 1 header\ngot  %q\nwant %q", got, target)
	}
	if degraded != 1 {
		t.Errorf("stage 1 header_degraded = %d, want 1", degraded)
	}

	// Stage 2: all callers dropped, callees kept.
	noCallers := in
	noCallers.Callers = nil
	target = fullHeader(noCallers)
	got, degraded = buildHeader(in, EstimateTokens(target))
	if got != target {
		t.Errorf("stage 2 header\ngot  %q\nwant %q", got, target)
	}
	if degraded != len(in.Callers) {
		t.Errorf("stage 2 header_degraded = %d, want %d", degraded, len(in.Callers))
	}

	// Stage 3: all callers and one callee dropped.
	oneCallee := noCallers
	oneCallee.Callees = in.Callees[:len(in.Callees)-1]
	target = fullHeader(oneCallee)
	got, degraded = buildHeader(in, EstimateTokens(target))
	if got != target {
		t.Errorf("stage 3 header\ngot  %q\nwant %q", got, target)
	}
	if degraded != len(in.Callers)+1 {
		t.Errorf("stage 3 header_degraded = %d, want %d", degraded, len(in.Callers)+1)
	}

	// Stage 4: lists empty and the signature line dropped.
	target = renderHeader(in.Package, in.File, "", nil, nil, true)
	got, degraded = buildHeader(in, EstimateTokens(target))
	if got != target {
		t.Errorf("stage 4 header\ngot  %q\nwant %q", got, target)
	}
	wantDegraded := len(in.Callers) + len(in.Callees) + 1
	if degraded != wantDegraded {
		t.Errorf("stage 4 header_degraded = %d, want %d", degraded, wantDegraded)
	}
	if strings.Contains(got, "// signature:") {
		t.Errorf("stage 4 header still has a signature line: %q", got)
	}
}
