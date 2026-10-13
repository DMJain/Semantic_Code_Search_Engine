package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/graph"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/store"
)

func runGraph(ctx context.Context, args []string) error {
	fs := newFlagSet("graph")
	pkg := fs.String("package", "", "graph only files of this import path (empty = every indexed file)")
	limit := fs.Int("limit", 0, "graph at most N stale files (0 = no limit)")
	stats := fs.Bool("stats", false, "print the edge reconciliation report")
	status := fs.Bool("status", false, "list the stale files and why, then exit")
	dryRun := fs.Bool("dry-run", false, "compute edges but write nothing (no-op writer)")
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
	return graphStage(ctx, pool, cfg.KubeDir, graphOptions{
		Package: *pkg, Limit: *limit, Stats: *stats, Status: *status, DryRun: *dryRun,
	}, os.Stdout)
}

// graphOptions configures one graph run.
type graphOptions struct {
	// Package limits the run to files of one import path (empty = all).
	Package string
	// Limit caps the number of stale files processed (0 = no limit).
	Limit int
	// Stats prints the reconciliation report.
	Stats bool
	// Status lists the stale files and why, then returns without graphing.
	Status bool
	// DryRun computes edges and stats but writes nothing (a no-op writer).
	DryRun bool
}

// graphFile is one files row selected for graphing, with the freshness markers
// it was last graphed at (nil = never graphed).
type graphFile struct {
	id         int64
	path       string
	pkg        string
	sha        string
	graphedSHA *string
	graphedFP  *string
}

// Stale reasons printed by --status and used to classify files.
const (
	reasonNeverGraphed  = "never graphed"
	reasonContentChange = "content changed"
	reasonSymbolsChange = "symbols changed"
)

// graphStatusCap caps the stale-file list --status prints.
const graphStatusCap = 50

// staleReasons returns why f is stale against the current fingerprint fp, or
// nil when fresh (D-076). A never-graphed file is reported as such alone.
func staleReasons(f graphFile, fp string) []string {
	if f.graphedSHA == nil {
		return []string{reasonNeverGraphed}
	}
	var reasons []string
	if *f.graphedSHA != f.sha {
		reasons = append(reasons, reasonContentChange)
	}
	if f.graphedFP == nil || *f.graphedFP != fp {
		reasons = append(reasons, reasonSymbolsChange)
	}
	return reasons
}

// graphStage re-parses the stale files and writes their tree-sitter edges, one
// transaction per file. Symbols are loaded once into memory. A stale file's
// whole package is extracted, so the declared-name index covers every file of
// the package even though only stale files are written (the freshness is per
// file). DB work uses a context a signal does not cancel, so the in-flight file
// always commits; the cancelled ctx is checked only at file boundaries (the
// parseCorpus pattern).
func graphStage(ctx context.Context, pool *pgxpool.Pool, root string, opts graphOptions, out io.Writer) error {
	dbCtx := context.WithoutCancel(ctx)

	symbols, err := loadSymbols(dbCtx, pool)
	if err != nil {
		return err
	}
	fp := graph.Fingerprint(symbols, graph.ResolverVersion)

	files, err := selectGraphFiles(dbCtx, pool, opts.Package)
	if err != nil {
		return err
	}

	if opts.Status {
		printGraphStatus(out, files, fp)
		return nil
	}

	// The symbol index is only needed to analyze files, so it is built after the
	// --status early return.
	ix := graph.NewIndex(symbols)

	stale := make(map[int64]bool)
	toGraph := 0
	for _, f := range files {
		if len(staleReasons(f, fp)) > 0 {
			stale[f.id] = true
			toGraph++
		}
	}
	fmt.Fprintf(out, "%d files to graph\n", toGraph)

	total := graph.NewStats()
	graphed := 0
groups:
	for _, group := range groupGraphFiles(files) {
		if ctx.Err() != nil || (opts.Limit > 0 && graphed >= opts.Limit) {
			break
		}
		if !groupHasStale(group, stale) {
			continue
		}
		// Extract every file of the package first, so the declared-name index
		// covers the whole package (a type in one file converts in another),
		// even though only the stale files are analyzed and written.
		infos := make([]graph.FileInfo, len(group))
		declared := map[string]string{}
		for i, f := range group {
			src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.path)))
			if err != nil {
				return fmt.Errorf("read %s: %w", f.path, err)
			}
			info, err := graph.Extract(src)
			if err != nil {
				return fmt.Errorf("extract %s: %w", f.path, err)
			}
			infos[i] = info
			for name, kind := range info.Declared {
				declared[name] = kind
			}
		}
		for i, f := range group {
			if ctx.Err() != nil || (opts.Limit > 0 && graphed >= opts.Limit) {
				break groups
			}
			if !stale[f.id] {
				continue
			}
			edges, st := graph.Analyze(f.path, f.pkg, infos[i], ix, declared)
			total.Add(st)
			if !opts.DryRun {
				if err := writeGraphFile(dbCtx, pool, f, edges, fp); err != nil {
					return err
				}
			}
			graphed++
		}
	}

	if err := total.Check(); err != nil {
		return fmt.Errorf("stats invariant: %w", err)
	}
	if opts.DryRun {
		fmt.Fprintf(out, "would graph %d files, %d edges\n", graphed, total.Stored())
	} else {
		fmt.Fprintf(out, "graphed %d files, stored %d edges\n", graphed, total.Stored())
	}
	if opts.Stats {
		printGraphStats(out, total)
	}
	return nil
}

// groupHasStale reports whether any file in an already package-ordered group is
// stale, so fresh packages are never read.
func groupHasStale(group []graphFile, stale map[int64]bool) bool {
	for _, f := range group {
		if stale[f.id] {
			return true
		}
	}
	return false
}

// printGraphStatus lists every stale file and why, with independent per-reason
// counts (a file can be both content changed and symbols changed, so these can
// sum above the total), the total, and the list capped (falling back to
// "0 files stale").
func printGraphStatus(out io.Writer, files []graphFile, fp string) {
	var stale []graphFile
	counts := map[string]int{}
	for _, f := range files {
		reasons := staleReasons(f, fp)
		if len(reasons) == 0 {
			continue
		}
		stale = append(stale, f)
		for _, reason := range reasons {
			counts[reason]++
		}
	}
	fmt.Fprintf(out, "%d files stale\n", len(stale))
	if len(stale) == 0 {
		return
	}
	fmt.Fprintf(out, "  reasons (a file may have more than one, so these sum above the total):\n")
	for _, reason := range []string{reasonNeverGraphed, reasonContentChange, reasonSymbolsChange} {
		fmt.Fprintf(out, "    %s: %d\n", reason, counts[reason])
	}
	for i, f := range stale {
		if i >= graphStatusCap {
			fmt.Fprintf(out, "  ... (%d more)\n", len(stale)-graphStatusCap)
			break
		}
		fmt.Fprintf(out, "  %s: %s\n", f.path, strings.Join(staleReasons(f, fp), ", "))
	}
}

// loadSymbols reads the whole symbol index, ordered by id, joined to its file
// so each symbol carries its package and path.
func loadSymbols(ctx context.Context, pool *pgxpool.Pool) ([]graph.Symbol, error) {
	rows, err := pool.Query(ctx, `
		SELECT s.id, s.file_id, f.path, f.package, s.name, s.qualified_name,
		       coalesce(s.receiver, ''), s.kind, s.start_line
		FROM symbols s JOIN files f ON f.id = s.file_id
		ORDER BY s.id`)
	if err != nil {
		return nil, fmt.Errorf("load symbols: %w", err)
	}
	defer rows.Close()

	var out []graph.Symbol
	for rows.Next() {
		var s graph.Symbol
		if err := rows.Scan(&s.ID, &s.FileID, &s.Path, &s.Package, &s.Name,
			&s.QualifiedName, &s.Receiver, &s.Kind, &s.StartLine); err != nil {
			return nil, fmt.Errorf("scan symbol: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// selectGraphFiles returns the files to consider, ordered by package then path,
// with the freshness markers they were last graphed at.
func selectGraphFiles(ctx context.Context, pool *pgxpool.Pool, pkg string) ([]graphFile, error) {
	q := `SELECT id, path, package, sha, graphed_sha, graphed_symbols_fp FROM files`
	var args []any
	if pkg != "" {
		q += ` WHERE package = $1`
		args = append(args, pkg)
	}
	q += ` ORDER BY package, path`

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	defer rows.Close()

	var out []graphFile
	for rows.Next() {
		var f graphFile
		if err := rows.Scan(&f.id, &f.path, &f.pkg, &f.sha, &f.graphedSHA, &f.graphedFP); err != nil {
			return nil, fmt.Errorf("scan file: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// groupGraphFiles splits files, already ordered by package then path, into
// contiguous per-package groups.
func groupGraphFiles(files []graphFile) [][]graphFile {
	var groups [][]graphFile
	for _, f := range files {
		last := len(groups) - 1
		if last < 0 || groups[last][0].pkg != f.pkg {
			groups = append(groups, []graphFile{f})
			continue
		}
		groups[last] = append(groups[last], f)
	}
	return groups
}

// writeGraphFile replaces one file's tree-sitter edges in a single transaction
// and records the content hash and fingerprint it was graphed at (D-076).
func writeGraphFile(ctx context.Context, pool *pgxpool.Pool, f graphFile, edges []graph.Edge, fp string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`DELETE FROM edges WHERE source = 'treesitter'
		   AND caller_id IN (SELECT id FROM symbols WHERE file_id = $1)`, f.id); err != nil {
		return fmt.Errorf("delete old edges for %s: %w", f.path, err)
	}

	for _, e := range edges {
		var callee any
		if e.CalleeID != 0 {
			callee = e.CalleeID
		}
		var reason any
		if e.UnresolvedReason != "" {
			reason = e.UnresolvedReason
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO edges (caller_id, callee_id, callee_raw, resolution, source, call_line, call_kind, unresolved_reason)
			 VALUES ($1, $2, $3, $4, 'treesitter', $5, $6, $7)
			 ON CONFLICT DO NOTHING`,
			e.CallerID, callee, e.CalleeRaw, e.Resolution, e.CallLine, e.CallKind, reason); err != nil {
			return fmt.Errorf("insert edge for %s: %w", f.path, err)
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE files SET graphed_sha = $2, graphed_symbols_fp = $3 WHERE id = $1`,
		f.id, f.sha, fp); err != nil {
		return fmt.Errorf("mark file graphed: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// printGraphStats writes the reconciliation report: counts per resolution and
// reason (summing to the stored total), per call kind, the skip and deferred
// counters, and the invariant line (spec stories 74-75).
func printGraphStats(out io.Writer, s graph.Stats) {
	fmt.Fprintf(out, "edges stored: %d\n", s.Stored())
	fmt.Fprintf(out, "  resolution exact: %d\n", s.Exact)
	fmt.Fprintf(out, "  resolution name_only: %d\n", s.NameOnly)
	fmt.Fprintf(out, "  resolution ambiguous: %d\n", s.Ambiguous)
	fmt.Fprintf(out, "  resolution unresolved: %d\n", s.Unresolved)
	for _, reason := range []string{
		graph.ReasonExternal, graph.ReasonNoMatch, graph.ReasonUnknownReceiver,
		graph.ReasonFuncValue, graph.ReasonCandidatesCap,
	} {
		fmt.Fprintf(out, "  reason %s: %d\n", reason, s.Reasons[reason])
	}
	fmt.Fprintf(out, "call kind func: %d\n", s.ByKind[graph.KindFunc])
	fmt.Fprintf(out, "call kind concrete_method: %d\n", s.ByKind[graph.KindConcreteMethod])
	fmt.Fprintf(out, "call kind interface_method: %d\n", s.ByKind[graph.KindInterfaceMethod])
	fmt.Fprintf(out, "call kind func_value: %d\n", s.ByKind[graph.KindFuncValue])
	fmt.Fprintf(out, "call kind unknown: %d\n", s.ByKind[graph.KindUnknown])

	percent := 0.0
	if stored := s.Stored(); stored > 0 {
		percent = 100 * float64(s.Ambiguous) / float64(stored)
	}
	fmt.Fprintf(out, "percent ambiguous: %.1f%%\n", percent)
	fmt.Fprintf(out, "unresolved: %d\n", s.Unresolved)
	fmt.Fprintf(out, "skipped_builtin: %d\n", s.SkippedBuiltin)
	fmt.Fprintf(out, "skipped_conversion: %d\n", s.SkippedConversion)
	fmt.Fprintf(out, "deferred_selector: %d\n", s.DeferredSelector)
	fmt.Fprintf(out, "deferred_local: %d\n", s.DeferredLocal)
	fmt.Fprintf(out, "deferred_interface: %d\n", s.DeferredInterface)
	fmt.Fprintf(out, "interfaces_partial: %d\n", s.InterfacesPartial)

	status := "ok"
	if err := s.Check(); err != nil {
		status = "FAILED"
	}
	fmt.Fprintf(out, "invariant: call sites %d = stored %d + skipped %d + deferred %d [%s]\n",
		s.CallSites, s.Stored(), s.Skipped(), s.Deferred(), status)
}
