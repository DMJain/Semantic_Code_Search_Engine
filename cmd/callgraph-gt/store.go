package main

import (
	"context"
	"encoding/json"
	"fmt"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/tools/go/ssa"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/graph"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/store"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/walk"
)

// gtSource is the edges.source value for the ground-truth graph (D-077).
const gtSource = "gopackages"

// Drop reasons for synthetic callers (story 17). The synthetic function's
// Synthetic text decides which one applies.
const (
	dropInit      = "init"
	dropBound     = "bound_method"
	dropThunk     = "thunk"
	dropWrapper   = "wrapper"
	dropSynthetic = "synthetic"
)

// gtStore is the GT-side partition printed by a store run. Total = Mapped +
// (sum of Dropped) + Unmapped (story 82); Check asserts it.
type gtStore struct {
	Total    int
	Mapped   int
	Unmapped int
	Rows     int
	Dropped  map[string]int
	ByKind   map[string]int
}

// check asserts the GT partition sums to the total (story 82).
func (s gtStore) check() error {
	dropped := 0
	for _, n := range s.Dropped {
		dropped += n
	}
	if s.Total != s.Mapped+dropped+s.Unmapped {
		return fmt.Errorf("gt partition does not sum: total %d != mapped %d + dropped %d + gt_unmapped %d",
			s.Total, s.Mapped, dropped, s.Unmapped)
	}
	return nil
}

// gtStoreResult is the committed JSON partition of the GT store run. Total =
// Mapped + sum(Dropped) + Unmapped, asserted before it is written.
type gtStoreResult struct {
	Total    int            `json:"total"`
	Mapped   int            `json:"mapped"`
	Unmapped int            `json:"gt_unmapped"`
	Rows     int            `json:"rows"`
	Dropped  map[string]int `json:"dropped"`
	ByKind   map[string]int `json:"by_kind"`
}

// writeGTStoreResult writes the GT partition result file, creating its
// directory. `eval callgraph` reads it back and asserts the partition.
func writeGTStoreResult(path string, st gtStore) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(gtStoreResult(st), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// runStore implements the default action: load the slice, build the VTA graph,
// map its in-slice edges to symbol ids and replace every source='gopackages'
// row in one transaction. It writes the partition to out when out is non-empty.
func runStore(out string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "callgraph-gt: %v\n", err)
		return 2
	}
	root, err := filepath.Abs(cfg.KubeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "callgraph-gt: %v\n", err)
		return 2
	}
	ctx := context.Background()
	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "callgraph-gt: %v\n", err)
		return 2
	}
	defer pool.Close()

	st, err := storeGT(ctx, pool, root, os.Environ())
	if err != nil {
		fmt.Fprintf(os.Stderr, "callgraph-gt: %v\n", err)
		return 2
	}
	printGTStore(os.Stdout, st)
	if err := st.check(); err != nil {
		fmt.Fprintf(os.Stderr, "callgraph-gt: %v\n", err)
		return 2
	}
	if out != "" {
		if err := writeGTStoreResult(out, st); err != nil {
			fmt.Fprintf(os.Stderr, "callgraph-gt: write %s: %v\n", out, err)
			return 2
		}
	}
	return 0
}

// pending is one mapped GT edge before deduplication and storage.
type pending struct {
	callerID int64
	calleeID int64
	raw      string
	kind     string
	line     int
}

// storeGT maps the in-slice VTA edges to symbols and replaces every
// source='gopackages' row in one transaction. It is factored out so a test can
// run it over the fixture module.
func storeGT(ctx context.Context, pool *pgxpool.Pool, root string, environ []string) (gtStore, error) {
	var st gtStore
	st.Dropped = map[string]int{}
	st.ByKind = map[string]int{}

	sg, err := buildSliceGraph(root, environ, rootsSliceAndSynthetic)
	if err != nil {
		return st, err
	}
	symbols, err := symbolIDs(ctx, pool)
	if err != nil {
		return st, err
	}

	// Pass 1 counts every in-slice edge; pass 2 assigns each to exactly one
	// partition bucket and the check compares the two. It is a structural sum
	// guard (each edge increments one bucket), not an independent recount, but
	// it still catches a categorisation that drops or double-counts an edge.
	for _, n := range sg.graph.Nodes {
		for _, e := range n.Out {
			if sg.inSliceEndpoint(e.Caller.Func) && sg.inSliceEndpoint(e.Callee.Func) {
				st.Total++
			}
		}
	}

	// Pass 2 classifies each in-slice edge exactly once and collects the mapped
	// ones.
	var mapped []pending
	for _, n := range sg.graph.Nodes {
		for _, e := range n.Out {
			caller, callee := e.Caller.Func, e.Callee.Func
			if !sg.inSliceEndpoint(caller) || !sg.inSliceEndpoint(callee) {
				continue
			}
			if isSyntheticCaller(caller) {
				st.Dropped[dropReason(caller)]++
				continue
			}
			callerQN, calleeQN := sg.declaredQN(caller), sg.declaredQN(callee)
			callerID, okCaller := symbols[callerQN]
			calleeID, okCallee := symbols[calleeQN]
			if !okCaller || !okCallee {
				st.Unmapped++
				continue
			}
			st.Mapped++
			mapped = append(mapped, pending{
				callerID: callerID, calleeID: calleeID, raw: calleeQN,
				kind: callKind(e.Site), line: siteLine(sg.prog, e.Site),
			})
		}
	}

	// Deduplicate on the edges_uniq columns after a total order, so the row kept
	// (and its kind) does not depend on map iteration order and two runs give
	// identical counts (story 23, 30).
	sort.Slice(mapped, func(i, j int) bool { return lessPending(mapped[i], mapped[j]) })
	ordered := make([]pending, 0, len(mapped))
	seen := map[edgeKey]bool{}
	for _, p := range mapped {
		k := edgeKey{p.callerID, p.calleeID, p.raw, p.line}
		if seen[k] {
			continue
		}
		seen[k] = true
		ordered = append(ordered, p)
		st.ByKind[p.kind]++
	}
	st.Rows = len(ordered)

	if err := replaceGT(ctx, pool, ordered); err != nil {
		return st, err
	}
	return st, nil
}

// lessPending totally orders mapped edges by the edges_uniq columns then kind,
// so deduplication keeps the lexicographically lowest kind per key.
func lessPending(a, b pending) bool {
	if a.callerID != b.callerID {
		return a.callerID < b.callerID
	}
	if a.calleeID != b.calleeID {
		return a.calleeID < b.calleeID
	}
	if a.raw != b.raw {
		return a.raw < b.raw
	}
	if a.line != b.line {
		return a.line < b.line
	}
	return a.kind < b.kind
}

// edgeKey is the deduplication key matching the edges_uniq columns (source is
// constant), so a repeated call on one line stores one row (story 30).
type edgeKey struct {
	callerID int64
	calleeID int64
	raw      string
	line     int
}

// replaceGT deletes every source='gopackages' row and inserts the mapped edges
// in one transaction, so a crash leaves the previous ground truth intact
// (story 23).
func replaceGT(ctx context.Context, pool *pgxpool.Pool, rows []pending) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM edges WHERE source = $1`, gtSource); err != nil {
		return fmt.Errorf("delete old gopackages edges: %w", err)
	}
	table := make([][]any, 0, len(rows))
	for _, p := range rows {
		table = append(table, []any{gtSource, p.callerID, p.calleeID, p.raw, graph.ResolutionExact, p.kind, p.line, nil})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"edges"},
		[]string{"source", "caller_id", "callee_id", "callee_raw", "resolution", "call_kind", "call_line", "unresolved_reason"},
		pgx.CopyFromRows(table)); err != nil {
		return fmt.Errorf("insert gopackages edges: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// symbolIDs loads the symbol index as qualified name -> id.
func symbolIDs(ctx context.Context, pool *pgxpool.Pool) (map[string]int64, error) {
	rows, err := pool.Query(ctx, `SELECT id, qualified_name FROM symbols`)
	if err != nil {
		return nil, fmt.Errorf("load symbols: %w", err)
	}
	defer rows.Close()

	out := map[string]int64{}
	for rows.Next() {
		var id int64
		var qn string
		if err := rows.Scan(&id, &qn); err != nil {
			return nil, fmt.Errorf("scan symbol: %w", err)
		}
		out[qn] = id
	}
	return out, rows.Err()
}

// inSliceEndpoint reports whether a graph endpoint belongs to the slice: a
// function with a position in a slice file, or a synthetic function of a slice
// package (the package-variable initialiser).
func (sg *sliceGraph) inSliceEndpoint(fn *ssa.Function) bool {
	return fnInSlice(sg.prog, fn, sg.sliceFiles) || syntheticSliceFn(fn, sg.slicePkgs)
}

// declaredQN maps an SSA function to the qualified name of its enclosing
// declared symbol: a generic instantiation maps to its origin, a closure to the
// enclosing declared function or method, pointer receivers preserved (story 15,
// 16, 18).
func (sg *sliceGraph) declaredQN(fn *ssa.Function) string {
	top := topLevel(fn)
	pkg := sg.fnPackage(top)
	if pkg == "" {
		return ""
	}
	if top.Signature != nil && top.Signature.Recv() != nil {
		recv, ok := recvName(top.Signature.Recv().Type())
		if !ok {
			return ""
		}
		return pkg + ".(" + recv + ")." + top.Name()
	}
	return pkg + "." + top.Name()
}

// fnPackage returns the symbol package (import path) of fn: walk.ImportPath of
// its in-slice file when it has a position, else its SSA package path.
func (sg *sliceGraph) fnPackage(fn *ssa.Function) string {
	if fn.Pos() != 0 {
		abs := sg.prog.Fset.Position(fn.Pos()).Filename
		if rel, _, included := inSliceRel(sg.root, abs); included {
			return walk.ImportPath(rel)
		}
	}
	if fn.Pkg != nil && fn.Pkg.Pkg != nil {
		return fn.Pkg.Pkg.Path()
	}
	return ""
}

// topLevel walks a function up its enclosing functions to the top-level
// declared function or method and, for a generic instantiation, resolves it to
// its origin.
func topLevel(fn *ssa.Function) *ssa.Function {
	for fn.Parent() != nil {
		fn = fn.Parent()
	}
	if fn.Origin() != nil {
		fn = fn.Origin()
	}
	return fn
}

// recvName renders a method receiver's type the way internal/parse does (story
// 15): a leading * for a pointer receiver and the type parameters preserved, so
// a generic method receiver such as (*Heap[T]) matches the stored qualified
// name. An instantiated receiver would render its concrete arguments, but the
// mapper resolves generic methods to their origin first.
func recvName(t types.Type) (string, bool) {
	ptr := false
	if p, ok := t.(*types.Pointer); ok {
		ptr = true
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return "", false
	}
	name := named.Obj().Name()
	if args := typeParams(named); args != "" {
		name += "[" + args + "]"
	}
	if ptr {
		name = "*" + name
	}
	return name, true
}

// typeParams renders a named type's type arguments (an instantiation) or its
// type parameters (a generic origin) as parse writes them in source, or "" when
// the type is not generic.
func typeParams(named *types.Named) string {
	targs := named.TypeArgs()
	if targs != nil && targs.Len() > 0 {
		return joinTypes(targs)
	}
	tparams := named.TypeParams()
	if tparams != nil && tparams.Len() > 0 {
		names := make([]string, tparams.Len())
		for i := 0; i < tparams.Len(); i++ {
			names[i] = tparams.At(i).Obj().Name()
		}
		return strings.Join(names, ", ")
	}
	return ""
}

// joinTypes renders a type argument list as source text, matching gofmt.
func joinTypes(args *types.TypeList) string {
	parts := make([]string, args.Len())
	for i := 0; i < args.Len(); i++ {
		parts[i] = args.At(i).String()
	}
	return strings.Join(parts, ", ")
}

// isSyntheticCaller reports whether fn is a synthetic wrapper or initialiser
// with no symbol of its own: a package-variable initialiser, bound-method or
// thunk wrapper. A generic instantiation has an origin and is not dropped.
func isSyntheticCaller(fn *ssa.Function) bool {
	return fn.Origin() == nil && fn.Parent() == nil && fn.Synthetic != ""
}

// dropReason classifies a synthetic caller for the dropped count (story 17).
func dropReason(fn *ssa.Function) string {
	s := fn.Synthetic
	switch {
	case strings.Contains(s, "package initializer"):
		return dropInit
	case strings.Contains(s, "bound method"):
		return dropBound
	case strings.Contains(s, "thunk"):
		return dropThunk
	case strings.Contains(s, "wrapper"):
		return dropWrapper
	}
	if fn.Name() == "init" {
		return dropInit
	}
	return dropSynthetic
}

// callKind is the SSA call mode (story 20): invoke is interface_method, a
// static callee with a receiver is concrete_method, a static callee without one
// is func, and a dynamic callee is func_value.
func callKind(site ssa.CallInstruction) string {
	if site == nil {
		return graph.KindUnknown
	}
	c := site.Common()
	if c.IsInvoke() {
		return graph.KindInterfaceMethod
	}
	if callee := c.StaticCallee(); callee != nil {
		if callee.Signature != nil && callee.Signature.Recv() != nil {
			return graph.KindConcreteMethod
		}
		return graph.KindFunc
	}
	return graph.KindFuncValue
}

// siteLine is the call site's line, 0 when unavailable (story 21).
func siteLine(prog *ssa.Program, site ssa.CallInstruction) int {
	if site == nil || site.Pos() == 0 {
		return 0
	}
	return prog.Fset.Position(site.Pos()).Line
}

// printGTStore prints the GT-side partition and the call-kind counts. The
// partition line names gt_unmapped so mapping loss is always visible (story 19,
// 82).
func printGTStore(out io.Writer, s gtStore) {
	dropped := 0
	for _, n := range s.Dropped {
		dropped += n
	}
	fmt.Fprintf(out, "gt edges: total %d = mapped %d (pre-dedup) + dropped %d + gt_unmapped %d\n",
		s.Total, s.Mapped, dropped, s.Unmapped)
	for _, reason := range sortedKeys(s.Dropped) {
		fmt.Fprintf(out, "  dropped %s: %d\n", reason, s.Dropped[reason])
	}
	for _, kind := range []string{graph.KindFunc, graph.KindConcreteMethod, graph.KindInterfaceMethod, graph.KindFuncValue, graph.KindUnknown} {
		fmt.Fprintf(out, "  call kind %s: %d\n", kind, s.ByKind[kind])
	}
	fmt.Fprintf(out, "  stored rows: %d (%d duplicate collapses)\n", s.Rows, s.Mapped-s.Rows)
}

// sortedKeys returns the map keys in ascending order, for deterministic output.
func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
