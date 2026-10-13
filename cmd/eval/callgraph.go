package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/graph"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/store"
)

// runCallgraph wires the DB queries to the pure scoring core: it refuses while
// any file is stale (D-074, the graph --status predicate), loads the two edge
// sources and the in-slice symbol set, scores and writes the JSON and markdown
// report.
func runCallgraph(ctx context.Context, args []string) error {
	fs := newFlagSet("callgraph")
	out := fs.String("out", config.DefaultCallgraphResultPath, "results JSON path")
	gtStore := fs.String("gt-store", config.DefaultGTStorePath, "GT partition result file written by callgraph-gt")
	sample := fs.Bool("sample", false, "write the seeded error sample instead of the precision/recall report")
	sampleOut := fs.String("sample-out", config.DefaultErrorSamplePath, "error sample JSON path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if *sample {
		return sampleCallgraph(ctx, pool, cfg.KubeDir, *sampleOut, os.Stdout)
	}
	return reportCallgraph(ctx, pool, cfg.KubeDir, *gtStore, *out, os.Stdout)
}

// reportCallgraph is runCallgraph without the flag parsing, so tests can point
// it at a throwaway database and temp files.
func reportCallgraph(ctx context.Context, pool *pgxpool.Pool, kubeDir, gtStorePath, out string, stdout io.Writer) error {
	stale, err := staleFileCount(ctx, pool, kubeDir)
	if err != nil {
		return err
	}
	if stale > 0 {
		return fmt.Errorf("%d files are stale (see `indexer graph --status`); graph the tree before scoring", stale)
	}

	in, err := loadScoreInput(ctx, pool)
	if err != nil {
		return err
	}
	rep, err := score(in)
	if err != nil {
		return err
	}
	part, err := loadGTPartition(gtStorePath)
	if err != nil {
		return err
	}
	if err := rep.assertGTPartition(part); err != nil {
		return err
	}
	if err := writeCallgraphResult(out, rep); err != nil {
		return err
	}
	fmt.Fprint(stdout, rep.markdown())
	return nil
}

// staleFileCount reproduces `indexer graph --status`: it builds the current
// fingerprint exactly as the graph path does and counts files whose content or
// symbols changed since they were graphed (D-076).
func staleFileCount(ctx context.Context, pool *pgxpool.Pool, root string) (int, error) {
	symbols, err := graph.LoadSymbols(ctx, pool)
	if err != nil {
		return 0, err
	}
	names, err := graph.BuildNameIndex(ctx, pool, root)
	if err != nil {
		return 0, err
	}
	fp := graph.Fingerprint(symbols, names, graph.ResolverVersion)

	rows, err := pool.Query(ctx, `SELECT sha, graphed_sha, graphed_symbols_fp FROM files`)
	if err != nil {
		return 0, fmt.Errorf("list files: %w", err)
	}
	defer rows.Close()

	stale := 0
	for rows.Next() {
		var sha string
		var graphedSHA, graphedFP *string
		if err := rows.Scan(&sha, &graphedSHA, &graphedFP); err != nil {
			return 0, fmt.Errorf("scan file: %w", err)
		}
		if len(graph.StaleReasons(sha, graphedSHA, graphedFP, fp)) > 0 {
			stale++
		}
	}
	return stale, rows.Err()
}

// tsRow is one stored tree-sitter edge row. The callee id alone classifies the
// row (unresolved iff it is 0, per the D-073 shape CHECK), so resolution is not
// carried.
type tsRow struct {
	CallerID  int64
	CalleeID  int64 // 0 = unresolved
	CalleeRaw string
	CallKind  string
	CallLine  int
	Reason    string
}

// gtRow is one stored ground-truth edge row (source='gopackages'). CallLine is
// carried only for the error sampler; scoring ignores it (D-074).
type gtRow struct {
	CallerID int64
	CalleeID int64
	CallKind string
	CallLine int
}

// scoreInput is the pure input the scorer works over, so tests can hand-build
// rows without Postgres.
type scoreInput struct {
	InSlice map[int64]bool // symbol id -> its file is in the slice
	TS      []tsRow
	GT      []gtRow
}

// loadScoreInput reads the two edge sources (filtered by source, story 88) and
// the in-slice symbol set.
func loadScoreInput(ctx context.Context, pool *pgxpool.Pool) (scoreInput, error) {
	in := scoreInput{InSlice: map[int64]bool{}}

	srows, err := pool.Query(ctx,
		`SELECT s.id FROM symbols s JOIN files f ON f.id = s.file_id WHERE f.in_slice`)
	if err != nil {
		return in, fmt.Errorf("load in-slice symbols: %w", err)
	}
	for srows.Next() {
		var id int64
		if err := srows.Scan(&id); err != nil {
			srows.Close()
			return in, fmt.Errorf("scan in-slice symbol: %w", err)
		}
		in.InSlice[id] = true
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return in, err
	}

	trows, err := pool.Query(ctx, `
		SELECT caller_id, coalesce(callee_id, 0), callee_raw, call_kind, call_line,
		       coalesce(unresolved_reason, '')
		FROM edges WHERE source = 'treesitter' ORDER BY id`)
	if err != nil {
		return in, fmt.Errorf("load tree-sitter edges: %w", err)
	}
	for trows.Next() {
		var r tsRow
		if err := trows.Scan(&r.CallerID, &r.CalleeID, &r.CalleeRaw,
			&r.CallKind, &r.CallLine, &r.Reason); err != nil {
			trows.Close()
			return in, fmt.Errorf("scan tree-sitter edge: %w", err)
		}
		in.TS = append(in.TS, r)
	}
	trows.Close()
	if err := trows.Err(); err != nil {
		return in, err
	}

	grows, err := pool.Query(ctx,
		`SELECT caller_id, callee_id, call_kind, call_line FROM edges WHERE source = 'gopackages' ORDER BY id`)
	if err != nil {
		return in, fmt.Errorf("load ground-truth edges: %w", err)
	}
	for grows.Next() {
		var r gtRow
		if err := grows.Scan(&r.CallerID, &r.CalleeID, &r.CallKind, &r.CallLine); err != nil {
			grows.Close()
			return in, fmt.Errorf("scan ground-truth edge: %w", err)
		}
		in.GT = append(in.GT, r)
	}
	grows.Close()
	return in, grows.Err()
}

// kindStat is the per-call-kind slice of the report. Rates with a zero
// denominator are undefined and marshal as null (n/a in the table):
// precision is over scored_units, recall over gt_pairs, and both
// ambiguous_rate and mean_candidates over all units of the kind (including
// unresolved and out-of-slice-resolved units, which dilute the mean).
type kindStat struct {
	Precision      *float64 `json:"precision"`
	Recall         *float64 `json:"recall"`
	AmbiguousRate  *float64 `json:"ambiguous_rate"`
	MeanCandidates *float64 `json:"mean_candidates"`
	// units / scored_units / ambiguous_units are tree-sitter call-site units;
	// gt_pairs / found are ground-truth pairs.
	Units          int `json:"units"`
	ScoredUnits    int `json:"scored_units"`
	AmbiguousUnits int `json:"ambiguous_units"`
	TP             int `json:"tp"`
	GTPairs        int `json:"gt_pairs"`
	Found          int `json:"found"`
}

// counts is the reconciliation block: the TS bucket partition, the GT partition
// read from the GT store file, and the TP/FP/FN totals.
type counts struct {
	TSEdges            int            `json:"ts_edges_in_slice_caller"`
	TSEdgesAll         int            `json:"ts_edges_all"`
	TSCallerOutOfSlice int            `json:"ts_caller_out_of_slice"`
	ScoredInSlice      int            `json:"ts_scored_in_slice"`
	ResolvedOutOfSlice int            `json:"ts_resolved_out_of_slice"`
	Unresolved         int            `json:"ts_unresolved"`
	UnresolvedByReason map[string]int `json:"ts_unresolved_by_reason"`
	OutOfSliceShare    *float64       `json:"out_of_slice_share"`

	PrecisionUniverse      int      `json:"precision_universe"`
	PrecisionExcludedShare *float64 `json:"precision_excluded_share"`
	TP                     int      `json:"tp"`
	FP                     int      `json:"fp"`

	RecallUniverse int `json:"recall_universe"`
	Found          int `json:"found"`
	FN             int `json:"fn"`

	GTTotal    int            `json:"gt_total"`
	GTMapped   int            `json:"gt_mapped"`
	GTUnmapped int            `json:"gt_unmapped"`
	GTDropped  map[string]int `json:"gt_dropped"`
	GTStored   int            `json:"gt_stored_rows"`
	GTByKind   map[string]int `json:"gt_by_kind"`
}

// callgraphReport is the committed result.json shape. Precision and Recall are
// null when their universe is empty (undefined, not zero). The unexported error
// lists are populated by score() so the error sampler reuses the exact scoring
// rules; they are not marshalled.
type callgraphReport struct {
	Scope     string              `json:"scope"`
	Note      string              `json:"note"`
	Precision *float64            `json:"precision"`
	Recall    *float64            `json:"recall"`
	ByKind    map[string]kindStat `json:"by_kind"`
	Counts    counts              `json:"counts"`

	fpUnits []fpUnit
	fnPairs []fnPair
}

// gtPartition mirrors the file callgraph-gt writes; its partition must sum.
type gtPartition struct {
	Total    int            `json:"total"`
	Mapped   int            `json:"mapped"`
	Unmapped int            `json:"gt_unmapped"`
	Rows     int            `json:"rows"`
	Dropped  map[string]int `json:"dropped"`
	ByKind   map[string]int `json:"by_kind"`
}

// callKinds is the stable report order and the set of buckets always present.
var callKinds = []string{
	graph.KindFunc, graph.KindConcreteMethod, graph.KindInterfaceMethod,
	graph.KindFuncValue, graph.KindUnknown,
}

// unit is one tree-sitter call site, collapsed by (caller, callee_raw, call_line).
type unit struct {
	caller int64
	kind   string
	raw    string
	line   int
	cands  map[int64]bool
}

type unitKey struct {
	caller int64
	raw    string
	line   int
}

type pair struct {
	caller int64
	callee int64
}

// score is the pure precision/recall core (D-074). It buckets every stored
// tree-sitter edge from an in-slice caller. Pass 1 counts the in-slice-caller
// edges and pass 2 assigns each to exactly one bucket; the guard comparing the
// two is a structural sum check (each row increments one bucket), not an
// independent recount, but it still catches a categorisation that drops or
// double-counts a row.
func score(in scoreInput) (callgraphReport, error) {
	rep := callgraphReport{
		Scope:  "in-slice only: caller and callee both in slice; identity is the (caller, callee) symbol pair",
		ByKind: map[string]kindStat{},
		Counts: counts{UnresolvedByReason: map[string]int{}, GTDropped: map[string]int{}, GTByKind: map[string]int{}},
	}
	for _, k := range callKinds {
		rep.ByKind[k] = kindStat{}
	}

	// Pass 1: count every stored tree-sitter edge whose caller is in slice
	// (story 80); pass 2 assigns each to exactly one bucket and the guard below
	// confirms the assignment covers every counted row.
	totalInSliceCaller := 0
	for _, r := range in.TS {
		if in.InSlice[r.CallerID] {
			totalInSliceCaller++
		}
	}

	// Pass 2: classify each in-slice-caller edge exactly once and collapse
	// resolved rows into call-site units.
	units := map[unitKey]*unit{}
	var unitOrder []unitKey
	scored, outOfSlice, unresolved := 0, 0, 0
	for _, r := range in.TS {
		rep.Counts.TSEdgesAll++
		if !in.InSlice[r.CallerID] {
			rep.Counts.TSCallerOutOfSlice++
			continue
		}
		switch {
		case r.CalleeID == 0:
			unresolved++
			rep.Counts.UnresolvedByReason[r.Reason]++
		case in.InSlice[r.CalleeID]:
			scored++
		default:
			outOfSlice++
		}
		k := unitKey{r.CallerID, r.CalleeRaw, r.CallLine}
		u := units[k]
		if u == nil {
			u = &unit{caller: r.CallerID, kind: r.CallKind, raw: r.CalleeRaw, line: r.CallLine, cands: map[int64]bool{}}
			units[k] = u
			unitOrder = append(unitOrder, k)
		}
		if r.CallKind != "" && r.CallKind < u.kind {
			u.kind = r.CallKind
		}
		if r.CalleeID != 0 {
			u.cands[r.CalleeID] = true
		}
	}

	rep.Counts.TSEdges = totalInSliceCaller
	rep.Counts.ScoredInSlice = scored
	rep.Counts.ResolvedOutOfSlice = outOfSlice
	rep.Counts.Unresolved = unresolved
	if scored+outOfSlice+unresolved != totalInSliceCaller {
		return rep, fmt.Errorf("tree-sitter partition does not sum: %d != scored %d + out-of-slice %d + unresolved %d",
			totalInSliceCaller, scored, outOfSlice, unresolved)
	}
	rep.Counts.OutOfSliceShare = rate(outOfSlice, totalInSliceCaller)

	// Ground-truth in-slice pairs: distinct (caller, callee) with both in slice,
	// attributed to the lexicographically lowest kind of its rows (mirroring the
	// GT store's dedup order) so by-kind universes sum to the overall.
	gtPairSet := map[pair]bool{}
	pairKind := map[pair]string{}
	pairLine := map[pair]int{}
	for _, r := range in.GT {
		rep.Counts.GTByKind[r.CallKind]++
		if !in.InSlice[r.CallerID] || !in.InSlice[r.CalleeID] {
			continue
		}
		p := pair{r.CallerID, r.CalleeID}
		if k, ok := pairKind[p]; !ok || (r.CallKind != "" && r.CallKind < k) {
			pairKind[p] = r.CallKind
		}
		if r.CallLine > 0 && (pairLine[p] == 0 || r.CallLine < pairLine[p]) {
			pairLine[p] = r.CallLine
		}
		gtPairSet[p] = true
	}
	rep.Counts.GTStored = len(in.GT)
	rep.Counts.RecallUniverse = len(gtPairSet)

	// Coverage index for recall: every callee any tree-sitter unit of a caller
	// names, including ambiguous candidates (story 83).
	covered := map[int64]map[int64]bool{}
	// Per-kind TS unit bookkeeping (all units, in-slice caller).
	unitsByKind := map[string]int{}
	scoredByKind := map[string]int{}
	ambByKind := map[string]int{}
	candByKind := map[string]int{}

	for _, k := range unitOrder {
		u := units[k]
		kind := u.kind
		if _, known := rep.ByKind[kind]; !known {
			rep.ByKind[kind] = kindStat{}
		}
		unitsByKind[kind]++
		candByKind[kind] += len(u.cands)
		if len(u.cands) > 1 {
			ambByKind[kind]++
		}
		if covered[u.caller] == nil {
			covered[u.caller] = map[int64]bool{}
		}
		inSliceCand := false
		for c := range u.cands {
			covered[u.caller][c] = true
			if in.InSlice[c] {
				inSliceCand = true
			}
		}
		if !inSliceCand {
			continue // resolved out of slice or unresolved: neither TP nor FP
		}
		rep.Counts.PrecisionUniverse++
		scoredByKind[kind]++
		tp := false
		for c := range u.cands {
			if gtPairSet[pair{u.caller, c}] {
				tp = true
				break
			}
		}
		if tp {
			st := rep.ByKind[kind]
			st.TP++
			rep.ByKind[kind] = st
			rep.Counts.TP++
		} else {
			// A scored unit no ground-truth pair matches: the false positives the
			// error sampler draws from (reusing this exact rule set).
			ids := make([]int64, 0, len(u.cands))
			for c := range u.cands {
				ids = append(ids, c)
			}
			slices.Sort(ids)
			rep.fpUnits = append(rep.fpUnits, fpUnit{CallerID: u.caller, Kind: kind, Raw: u.raw, Line: u.line, CandIDs: ids})
		}
	}
	rep.Counts.FP = rep.Counts.PrecisionUniverse - rep.Counts.TP
	totalUnits := 0
	for _, n := range unitsByKind {
		totalUnits += n
	}
	rep.Counts.PrecisionExcludedShare = rate(totalUnits-rep.Counts.PrecisionUniverse, totalUnits)

	// Recall per GT pair, attributed by the pair's kind.
	for p := range gtPairSet {
		kind := pairKind[p]
		st := rep.ByKind[kind]
		st.GTPairs++
		st.Units = unitsByKind[kind]
		st.ScoredUnits = scoredByKind[kind]
		st.AmbiguousUnits = ambByKind[kind]
		if covered[p.caller][p.callee] {
			st.Found++
			rep.Counts.Found++
		} else {
			// An in-slice ground-truth pair no unit covers: the false negatives
			// the error sampler draws from.
			rep.fnPairs = append(rep.fnPairs, fnPair{CallerID: p.caller, CalleeID: p.callee, Kind: kind, Line: pairLine[p]})
		}
		rep.ByKind[kind] = st
	}
	rep.Counts.FN = rep.Counts.RecallUniverse - rep.Counts.Found

	// Fill the TS-side counters for kinds that had no GT pair, then derive the
	// per-kind rates. A rate with a zero denominator is left nil (undefined).
	for _, k := range callKinds {
		st := rep.ByKind[k]
		st.Units = unitsByKind[k]
		st.ScoredUnits = scoredByKind[k]
		st.AmbiguousUnits = ambByKind[k]
		st.Precision = rate(st.TP, st.ScoredUnits)
		st.Recall = rate(st.Found, st.GTPairs)
		st.AmbiguousRate = rate(st.AmbiguousUnits, st.Units)
		st.MeanCandidates = rate(candByKind[k], st.Units)
		rep.ByKind[k] = st
	}

	rep.Precision = rate(rep.Counts.TP, rep.Counts.PrecisionUniverse)
	rep.Recall = rate(rep.Counts.Found, rep.Counts.RecallUniverse)

	share := func(n, d int) string {
		if d == 0 {
			return "n/a"
		}
		return fmt.Sprintf("%.4f", float64(n)/float64(d))
	}
	rep.Note = fmt.Sprintf(
		"in-slice only; out-of-slice share %s (%d of %d stored tree-sitter edges from in-slice callers resolved out of slice); "+
			"resolved out-of-slice and unresolved units are neither TP nor FP (excluded share %s); "+
			"precision is over scored_units, recall over gt_pairs, ambiguous_rate and mean_candidates over all units of the kind (so zero-candidate units dilute the mean); "+
			"a rate with a zero denominator is undefined and is null (n/a in the table); skip counters are in `indexer graph --stats`",
		share(rep.Counts.ResolvedOutOfSlice, rep.Counts.TSEdges), rep.Counts.ResolvedOutOfSlice, rep.Counts.TSEdges,
		share(totalUnits-rep.Counts.PrecisionUniverse, totalUnits))
	return rep, nil
}

// rate returns num/den, or nil when den is zero: the rate is undefined, not
// zero, and marshals as JSON null.
func rate(num, den int) *float64 {
	if den == 0 {
		return nil
	}
	v := float64(num) / float64(den)
	return &v
}

// assertGTPartition asserts the GT store's partition sums to its total and that
// the stored rows the report scored match the file (story 82). It records the
// partition on the report so the written JSON carries it.
func (r *callgraphReport) assertGTPartition(p gtPartition) error {
	dropped := 0
	for _, n := range p.Dropped {
		dropped += n
	}
	if p.Total != p.Mapped+dropped+p.Unmapped {
		return fmt.Errorf("gt partition does not sum: total %d != mapped %d + dropped %d + gt_unmapped %d",
			p.Total, p.Mapped, dropped, p.Unmapped)
	}
	if p.Rows != r.Counts.GTStored {
		return fmt.Errorf("gt store file rows %d != stored gopackages rows %d", p.Rows, r.Counts.GTStored)
	}
	if !sameCounts(p.ByKind, r.Counts.GTByKind) {
		return fmt.Errorf("gt store by_kind %v != stored gopackages by kind %v", p.ByKind, r.Counts.GTByKind)
	}
	r.Counts.GTTotal = p.Total
	r.Counts.GTMapped = p.Mapped
	r.Counts.GTUnmapped = p.Unmapped
	r.Counts.GTDropped = p.Dropped
	return nil
}

// sameCounts reports whether two string->count maps are equal, treating a
// missing key as zero so an explicit zero and an absent key agree.
func sameCounts(a, b map[string]int) bool {
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	for k, v := range b {
		if a[k] != v {
			return false
		}
	}
	return true
}

// loadGTPartition reads the GT store result file.
func loadGTPartition(path string) (gtPartition, error) {
	var p gtPartition
	data, err := os.ReadFile(path)
	if err != nil {
		return p, fmt.Errorf("read GT partition %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return p, fmt.Errorf("parse GT partition %s: %w", path, err)
	}
	return p, nil
}

// markdown renders the by-kind table plus the scope and partition lines.
func (r callgraphReport) markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Call-graph precision/recall (in-slice only).** %s\n\n", r.Scope)
	fmt.Fprintf(&b, "| kind | precision | recall | ambiguous rate | mean candidates |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- | --- |\n")
	fmt.Fprintf(&b, "| **overall** | %s | %s | | |\n", f4(r.Precision), f4(r.Recall))
	for _, k := range callKinds {
		st := r.ByKind[k]
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
			k, f4(st.Precision), f4(st.Recall), f4(st.AmbiguousRate), f4(st.MeanCandidates))
	}
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "TS buckets: scored_in_slice %d, resolved_out_of_slice %d, unresolved %d (sum %d)\n",
		r.Counts.ScoredInSlice, r.Counts.ResolvedOutOfSlice, r.Counts.Unresolved, r.Counts.TSEdges)
	fmt.Fprintf(&b, "unresolved by reason: %s\n", formatReasonCounts(r.Counts.UnresolvedByReason))
	fmt.Fprintf(&b, "GT partition: total %d = mapped %d + dropped %d + gt_unmapped %d\n",
		r.Counts.GTTotal, r.Counts.GTMapped, droppedSum(r.Counts.GTDropped), r.Counts.GTUnmapped)
	fmt.Fprintf(&b, "precision %d/%d TP, recall %d/%d found\n",
		r.Counts.TP, r.Counts.PrecisionUniverse, r.Counts.Found, r.Counts.RecallUniverse)
	fmt.Fprintf(&b, "out-of-slice share %s; precision-excluded share %s\n",
		f4(r.Counts.OutOfSliceShare), f4(r.Counts.PrecisionExcludedShare))
	fmt.Fprintf(&b, "rates: precision over scored_units, recall over gt_pairs, ambiguous rate and mean candidates over all units of the kind; a zero denominator is n/a\n")
	return b.String()
}

func droppedSum(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// formatReasonCounts renders unresolved-by-reason counts in a stable order.
func formatReasonCounts(m map[string]int) string {
	reasons := []string{
		graph.ReasonExternal, graph.ReasonNoMatch, graph.ReasonUnknownReceiver,
		graph.ReasonFuncValue, graph.ReasonCandidatesCap,
	}
	parts := make([]string, 0, len(reasons))
	for _, r := range reasons {
		parts = append(parts, fmt.Sprintf("%s=%d", r, m[r]))
	}
	return strings.Join(parts, " ")
}

// f4 formats a rate to four decimals for the markdown table, or n/a when the
// rate is undefined (a nil pointer, a zero denominator) or not finite.
func f4(v *float64) string {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return "n/a"
	}
	return fmt.Sprintf("%.4f", *v)
}

// writeCallgraphResult writes the report JSON, creating its directory.
func writeCallgraphResult(path string, rep callgraphReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
