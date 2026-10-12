package summarize

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Draft prompt versions used only by the throughput benchmark. File-
// and package-level summaries are S5; these throwaway versions label the
// synthetic prompts timed here and are never written to the summaries table.
const (
	FileBenchPromptVersion = "file-bench"
	PkgBenchPromptVersion  = "pkg-bench"
)

// FileBenchSystemPrompt is the draft file-level system message. It is synthetic
// (S5 owns real file summaries) and exists only so file-level latency is
// measured against a realistic instruction.
const FileBenchSystemPrompt = "You are summarizing a Go source file from Kubernetes for a code search index. Write 2-3 sentences describing the file's purpose and the behavior of its main functions. No code, no preamble."

// PkgBenchSystemPrompt is the draft package-level system message (synthetic; S5
// owns real package summaries).
const PkgBenchSystemPrompt = "You are summarizing a Go package from Kubernetes for a code search index. Write 3-4 sentences describing the package's purpose and its main behaviors. No code, no preamble."

// FileCandidate is one in-slice file offered to [SampleFiles]: its path, its
// package and its count of eligible functions.
type FileCandidate struct {
	Path     string
	Package  string
	Eligible int
}

// BenchOptions are the inputs to [Runner.Bench]. The zero value samples nothing.
type BenchOptions struct {
	// FunctionN is how many eligible functions the fn-v1 benchmark draws.
	FunctionN int
	// FileN is how many files the file-level benchmark draws.
	FileN int
	// PkgN is how many packages the package-level benchmark times.
	PkgN int
	// Seed is the sampling seed.
	Seed int64
}

// BenchResult is the JSON written to eval/results/bench/summarize.json. All
// latencies are milliseconds and all throughputs are per hour.
type BenchResult struct {
	Model       string  `json:"model"`
	NumCtx      int     `json:"num_ctx"`
	Seed        int64   `json:"seed"`
	GeneratedAt string  `json:"generated_at"`
	WallSeconds float64 `json:"wall_seconds"`

	FnPerHourC1      float64 `json:"fn_per_hour_c1"`
	FnPerHourC2      float64 `json:"fn_per_hour_c2"`
	FnP50MsC1        float64 `json:"fn_p50_ms_c1"`
	FnP95MsC1        float64 `json:"fn_p95_ms_c1"`
	FnP50MsC2        float64 `json:"fn_p50_ms_c2"`
	FnP95MsC2        float64 `json:"fn_p95_ms_c2"`
	FnOutputTokensC1 int     `json:"fn_output_tokens_c1"`
	FnOutputTokensC2 int     `json:"fn_output_tokens_c2"`
	FnDoneC1         int     `json:"fn_done_c1"`
	FnFailedC1       int     `json:"fn_failed_c1"`
	FnDoneC2         int     `json:"fn_done_c2"`
	FnFailedC2       int     `json:"fn_failed_c2"`

	FilePerHour      float64 `json:"file_per_hour"`
	FileP50Ms        float64 `json:"file_p50_ms"`
	FileP95Ms        float64 `json:"file_p95_ms"`
	FileOutputTokens int     `json:"file_output_tokens"`
	FileDone         int     `json:"file_done"`
	FileFailed       int     `json:"file_failed"`
	PkgPerHour       float64 `json:"pkg_per_hour"`
	PkgP50Ms         float64 `json:"pkg_p50_ms"`
	PkgP95Ms         float64 `json:"pkg_p95_ms"`
	PkgOutputTokens  int     `json:"pkg_output_tokens"`
	PkgDone          int     `json:"pkg_done"`
	PkgFailed        int     `json:"pkg_failed"`

	SkippedFns       int      `json:"skipped_fns"`
	SliceFnsEligible int      `json:"slice_fns_eligible"`
	FileStrata       []string `json:"file_strata"`
	FileStrataCount  int      `json:"file_strata_count"`

	Note     string            `json:"note"`
	Files    []BenchFileResult `json:"files"`
	Packages []BenchPkgResult  `json:"packages"`
}

// BenchFileResult is one sampled file's file-level measurement.
type BenchFileResult struct {
	Path         string  `json:"path"`
	Package      string  `json:"package"`
	Eligible     int     `json:"eligible"`
	Stratum      string  `json:"stratum"`
	LatencyMs    float64 `json:"latency_ms"`
	OutputTokens int     `json:"output_tokens"`
	OK           bool    `json:"ok"`
}

// BenchPkgResult is one timed package's package-level measurement.
type BenchPkgResult struct {
	Package      string  `json:"package"`
	Files        int     `json:"files"`
	LatencyMs    float64 `json:"latency_ms"`
	OutputTokens int     `json:"output_tokens"`
	OK           bool    `json:"ok"`
}

// Bench measures summarization throughput without writing any summary row. It samples FunctionN eligible functions (seed) with the same
// stratified [Sample] as production, runs the exact fn-v1 prompt at
// concurrency 1 and then 2, then times FileN stratified file-level prompts and
// PkgN package-level prompts with the draft file-bench and pkg-bench prompts.
// Each file/package input is built from the fn-v1 summaries of the file's
// eligible functions, which are not stored.
func (r *Runner) Bench(ctx context.Context, opts BenchOptions) (BenchResult, error) {
	start := time.Now()
	out := r.out()
	res := BenchResult{
		Model:       r.Model,
		NumCtx:      r.Client.numCtx,
		Seed:        opts.Seed,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Note: "File- and package-level prompts are throwaway drafts (" +
			FileBenchPromptVersion + ", " + PkgBenchPromptVersion + "); " +
			"hierarchical summaries are S5. Their input text is built from the fn-v1 summaries " +
			"of the file's eligible functions, which are not stored. Nothing is written to the " +
			"summaries table.",
	}

	rows, err := r.eligible(ctx)
	if err != nil {
		return res, err
	}
	res.SliceFnsEligible = len(rows)
	if res.SkippedFns, err = r.countSkipped(ctx); err != nil {
		return res, err
	}
	pkgs, err := r.inSlicePackages(ctx)
	if err != nil {
		return res, err
	}
	byPath := make(map[string][]symbolRow)
	for _, sr := range rows {
		byPath[sr.path] = append(byPath[sr.path], sr)
	}

	// File sample stratified by eligible-function count (reported before the
	// long function runs so a failure is visible early).
	cands := make([]FileCandidate, 0, len(byPath))
	for path, group := range byPath {
		cands = append(cands, FileCandidate{Path: path, Package: pkgs[path], Eligible: len(group)})
	}
	sampleFiles := SampleFiles(cands, opts.FileN, opts.Seed)
	fmt.Fprintf(out, "bench: %d sampled files across %d strata (seed %d)\n",
		len(sampleFiles), len(distinctStrata(sampleFiles)), opts.Seed)

	// Function benchmark: the same 200 sampled functions at c1 then c2.
	ids := Sample(sampleCandidates(rows), opts.FunctionN, opts.Seed)
	fnRows := filterByID(rows, ids)
	fnInputs := make([]Input, 0, len(fnRows))
	for _, sr := range fnRows {
		in, err := BuildInput(r.KubeDir, sr.path, sr.sym)
		if err != nil {
			return res, fmt.Errorf("read %s: %w", sr.path, err)
		}
		fnInputs = append(fnInputs, in)
	}
	fmt.Fprintf(out, "bench: %d sampled functions (seed %d)\n", len(fnInputs), opts.Seed)

	c1, wall1 := runCalls(1, fnCalls(ctx, r, fnInputs))
	c2, wall2 := runCalls(2, fnCalls(ctx, r, fnInputs))
	res.FnPerHourC1 = perHour(len(fnInputs), wall1)
	res.FnPerHourC2 = perHour(len(fnInputs), wall2)
	res.FnP50MsC1, res.FnP95MsC1 = percentilesMS(c1)
	res.FnP50MsC2, res.FnP95MsC2 = percentilesMS(c2)
	res.FnOutputTokensC1 = meanOutputTokens(c1)
	res.FnOutputTokensC2 = meanOutputTokens(c2)
	res.FnDoneC1, res.FnFailedC1 = countOK(c1)
	res.FnDoneC2, res.FnFailedC2 = countOK(c2)
	fmt.Fprintf(out, "bench: fn c1 %.1f/h c2 %.1f/h\n", res.FnPerHourC1, res.FnPerHourC2)

	// Package inputs: the sampled files grouped by package, plus fallback files
	// from unseen packages if the sample spans fewer than PkgN packages.
	pkgFiles := make(map[string][]FileCandidate)
	for _, f := range sampleFiles {
		pkgFiles[f.Package] = append(pkgFiles[f.Package], f)
	}
	if len(pkgFiles) < opts.PkgN {
		ordered := slices.Clone(cands)
		sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
		for _, f := range ordered {
			if len(pkgFiles) >= opts.PkgN {
				break
			}
			if _, ok := pkgFiles[f.Package]; !ok {
				pkgFiles[f.Package] = []FileCandidate{f}
			}
		}
	}

	// Build file inputs: every file that a file or package prompt needs has all
	// its eligible functions summarized with fn-v1 first (not stored).
	inputFiles := flattenFiles(pkgFiles)
	fileSummaries, err := r.summarizeFileInputs(ctx, inputFiles, byPath)
	if err != nil {
		return res, err
	}
	idxOf := make(map[string]int, len(inputFiles))
	fileUser := make([]string, len(inputFiles))
	for i, f := range inputFiles {
		idxOf[f.Path] = i
		fileUser[i] = fileBenchUserMessage(f.Path, byPath[f.Path], fileSummaries[i])
	}

	fileCalls := make([]func() Result, len(sampleFiles))
	for i, f := range sampleFiles {
		user := fileUser[idxOf[f.Path]]
		fileCalls[i] = func() Result { return r.Client.Summarize(ctx, FileBenchSystemPrompt, user) }
	}
	fileSamples, fileWall := runCalls(1, fileCalls)
	res.FilePerHour = perHour(len(sampleFiles), fileWall)
	res.FileP50Ms, res.FileP95Ms = percentilesMS(fileSamples)
	res.FileOutputTokens = meanOutputTokens(fileSamples)
	res.FileDone, res.FileFailed = countOK(fileSamples)
	for i, f := range sampleFiles {
		res.Files = append(res.Files, BenchFileResult{
			Path:         f.Path,
			Package:      f.Package,
			Eligible:     f.Eligible,
			Stratum:      FileStratum(f.Eligible),
			LatencyMs:    ms(fileSamples[i].latency),
			OutputTokens: fileSamples[i].result.OutputTokens,
			OK:           fileSamples[i].result.OK(),
		})
	}
	res.FileStrata = distinctStrata(sampleFiles)
	res.FileStrataCount = len(res.FileStrata)

	pkgNames := make([]string, 0, len(pkgFiles))
	for name := range pkgFiles {
		pkgNames = append(pkgNames, name)
	}
	sort.Strings(pkgNames)
	if len(pkgNames) > opts.PkgN {
		pkgNames = pkgNames[:opts.PkgN]
	}
	pkgCalls := make([]func() Result, len(pkgNames))
	for i, name := range pkgNames {
		files := slices.Clone(pkgFiles[name])
		sort.SliceStable(files, func(a, b int) bool { return files[a].Path < files[b].Path })
		var b strings.Builder
		fmt.Fprintf(&b, "Package: %s\n", name)
		for _, f := range files {
			fmt.Fprintf(&b, "\nFile %s:\n%s\n", f.Path, fileUser[idxOf[f.Path]])
		}
		user := b.String()
		pkgCalls[i] = func() Result { return r.Client.Summarize(ctx, PkgBenchSystemPrompt, user) }
	}
	pkgSamples, pkgWall := runCalls(1, pkgCalls)
	res.PkgPerHour = perHour(len(pkgNames), pkgWall)
	res.PkgP50Ms, res.PkgP95Ms = percentilesMS(pkgSamples)
	res.PkgOutputTokens = meanOutputTokens(pkgSamples)
	res.PkgDone, res.PkgFailed = countOK(pkgSamples)
	for i, name := range pkgNames {
		res.Packages = append(res.Packages, BenchPkgResult{
			Package:      name,
			Files:        len(pkgFiles[name]),
			LatencyMs:    ms(pkgSamples[i].latency),
			OutputTokens: pkgSamples[i].result.OutputTokens,
			OK:           pkgSamples[i].result.OK(),
		})
	}
	fmt.Fprintf(out, "bench: file %.1f/h pkg %.1f/h\n", res.FilePerHour, res.PkgPerHour)

	res.WallSeconds = time.Since(start).Seconds()
	return res, nil
}

// WriteBenchResult writes res as indented JSON to path, creating parent
// directories.
func WriteBenchResult(path string, res BenchResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// countSkipped counts in-slice function/method symbols whose body is shorter
// than the eligibility minimum ([MinBodyLines], D-014).
func (r *Runner) countSkipped(ctx context.Context) (int, error) {
	var n int
	err := r.Pool.QueryRow(ctx, `
		SELECT count(*) FROM symbols s
		JOIN files f ON f.id = s.file_id
		WHERE `+SkippedWhere()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count skipped functions: %w", err)
	}
	return n, nil
}

// inSlicePackages returns the package name of every in-slice file keyed by path.
func (r *Runner) inSlicePackages(ctx context.Context) (map[string]string, error) {
	rows, err := r.Pool.Query(ctx, `SELECT path, package FROM files WHERE in_slice`)
	if err != nil {
		return nil, fmt.Errorf("list in-slice packages: %w", err)
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var path, pkg string
		if err := rows.Scan(&path, &pkg); err != nil {
			return nil, fmt.Errorf("scan file package: %w", err)
		}
		out[path] = pkg
	}
	return out, rows.Err()
}

// summarizeFileInputs summarizes every eligible function of the input files
// with the production fn-v1 prompt at concurrency 2 (not stored) and returns,
// per file, the summaries in the file's symbol order. It is the file/package
// input-building pass, so its cost is not attributed to file or package
// latency.
func (r *Runner) summarizeFileInputs(ctx context.Context, files []FileCandidate, byPath map[string][]symbolRow) ([][]string, error) {
	type job struct {
		fileIdx int
		user    string
	}
	var jobs []job
	for i, f := range files {
		for _, sr := range byPath[f.Path] {
			in, err := BuildInput(r.KubeDir, sr.path, sr.sym)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", sr.path, err)
			}
			jobs = append(jobs, job{fileIdx: i, user: in.UserMessage()})
		}
	}
	calls := make([]func() Result, len(jobs))
	for j := range jobs {
		user := jobs[j].user
		calls[j] = func() Result { return r.Client.Summarize(ctx, SystemPrompt, user) }
	}
	samples, _ := runCalls(2, calls)
	out := make([][]string, len(files))
	for j, s := range samples {
		out[jobs[j].fileIdx] = append(out[jobs[j].fileIdx], s.result.Text)
	}
	return out, nil
}

// fnCalls builds one call per input for the fn-v1 benchmark.
func fnCalls(ctx context.Context, r *Runner, inputs []Input) []func() Result {
	calls := make([]func() Result, len(inputs))
	for i := range inputs {
		user := inputs[i].UserMessage()
		calls[i] = func() Result { return r.Client.Summarize(ctx, SystemPrompt, user) }
	}
	return calls
}

// fileBenchUserMessage renders the draft file-level user message: the path and,
// per symbol, its signature and fn-v1 summary.
func fileBenchUserMessage(path string, group []symbolRow, summaries []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "File: %s\n", path)
	for i, sr := range group {
		summary := "(summary unavailable)"
		if i < len(summaries) && summaries[i] != "" {
			summary = summaries[i]
		}
		fmt.Fprintf(&b, "\n%s\n%s\n", sr.sym.Signature, summary)
	}
	return b.String()
}

// flattenFiles returns the unique candidates from pkgFiles sorted by path.
func flattenFiles(pkgFiles map[string][]FileCandidate) []FileCandidate {
	seen := make(map[string]bool)
	var out []FileCandidate
	for _, files := range pkgFiles {
		for _, f := range files {
			if !seen[f.Path] {
				seen[f.Path] = true
				out = append(out, f)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// distinctStrata returns the distinct [FileStratum] labels among files, in the
// smallest-to-largest order.
func distinctStrata(files []FileCandidate) []string {
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		seen[FileStratum(f.Eligible)] = true
	}
	var out []string
	for _, s := range fileStrataOrder {
		if seen[s] {
			out = append(out, s)
		}
	}
	return out
}

// callSample is one measured request: how long it took and its outcome.
type callSample struct {
	latency time.Duration
	result  Result
}

// runCalls runs calls with at most concurrency workers, recording each call's
// latency and result, and returns the samples in call order plus the wall time.
func runCalls(concurrency int, calls []func() Result) ([]callSample, time.Duration) {
	if concurrency < 1 {
		concurrency = 1
	}
	out := make([]callSample, len(calls))
	start := time.Now()
	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				t0 := time.Now()
				res := calls[j]()
				out[j] = callSample{latency: time.Since(t0), result: res}
			}
		}()
	}
	for j := range calls {
		jobs <- j
	}
	close(jobs)
	wg.Wait()
	return out, time.Since(start)
}

// perHour converts n completed items over wall into items per hour.
func perHour(n int, wall time.Duration) float64 {
	if n == 0 || wall <= 0 {
		return 0
	}
	return float64(n) / wall.Seconds() * 3600
}

// percentilesMS returns the p50 and p95 latency in milliseconds over the
// successful calls (a too_large or failed call has no meaningful latency).
func percentilesMS(samples []callSample) (p50, p95 float64) {
	var ds []time.Duration
	for _, s := range samples {
		if s.result.OK() {
			ds = append(ds, s.latency)
		}
	}
	return percentileMS(ds, 50), percentileMS(ds, 95)
}

// percentileMS returns the nearest-rank percentile p (0-100) of ds in
// milliseconds; it is 0 for an empty slice.
func percentileMS(ds []time.Duration, p float64) float64 {
	if len(ds) == 0 {
		return 0
	}
	xs := make([]float64, len(ds))
	for i, d := range ds {
		xs[i] = ms(d)
	}
	sort.Float64s(xs)
	idx := int(math.Ceil(p/100*float64(len(xs)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(xs) {
		idx = len(xs) - 1
	}
	return xs[idx]
}

// meanOutputTokens returns the mean generated-token count over the successful
// calls, rounded to the nearest integer.
func meanOutputTokens(samples []callSample) int {
	sum, n := 0, 0
	for _, s := range samples {
		if s.result.OK() {
			sum += s.result.OutputTokens
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return int(math.Round(float64(sum) / float64(n)))
}

// countOK returns the number of successful and failed calls.
func countOK(samples []callSample) (done, failed int) {
	for _, s := range samples {
		if s.result.OK() {
			done++
		} else {
			failed++
		}
	}
	return done, failed
}

func ms(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}
