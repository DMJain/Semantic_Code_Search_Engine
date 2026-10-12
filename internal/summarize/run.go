package summarize

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"slices"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/seeded"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/store"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/walk"
)

// Runner summarizes eligible in-slice functions into the summaries table. It is
// the database-backed half of the summarize stage (S1-T10): each item is
// written pending, sent to the LLM, then finalized done or failed, so an
// interrupt never leaves a partial row. Reusing [Client] keeps the Ollama call
// and the D-015 guards in one place.
type Runner struct {
	// Pool is the Postgres pool the summaries are written to.
	Pool *pgxpool.Pool
	// Client summarizes one function at a time.
	Client *Client
	// KubeDir is the checkout root the function code is read from.
	KubeDir string
	// Model is the summary model recorded on every row.
	Model string
	// Out receives the run report; nil means io.Discard.
	Out io.Writer
}

// RunOptions are the per-invocation controls for [Runner.Run]. The zero value
// summarizes every eligible row that still needs work.
type RunOptions struct {
	// Limit caps the selection by symbol id after sampling but before
	// skipping, so rerunning the same command re-picks the same rows and the
	// already-done ones are skipped (a rerun is a no-op); 0 means no cap.
	Limit int
	// Sample draws this many eligible functions, stratified across the slice
	// roots (0 means every eligible row).
	Sample int
	// Seed is the sampling seed.
	Seed int64
	// Concurrency is the number of parallel summary requests (1 or 2).
	Concurrency int
	// RetryFailed re-picks rows that previously failed.
	RetryFailed bool
	// PrintSample prints the sampled ids and stops before summarizing.
	PrintSample bool
}

// Stats reports one summarize run.
type Stats struct {
	Eligible int
	Selected int
	Skipped  int
	Done     int
	Failed   int
	Pending  int
}

// workItem is one function queued for summarization: its row, the prompt input
// and the input hash recomputed from the current symbol.
type workItem struct {
	row   symbolRow
	input Input
	hash  string
}

// symbolRow is one in-slice symbol as read from the database together with its
// stored summary status and hash (empty when there is no current row).
type symbolRow struct {
	id            int64
	qualifiedName string
	path          string
	sym           parse.Symbol
	storedStatus  string
	storedHash    string
}

// Run selects eligible functions, applies sampling, skipping and the limit,
// summarizes the remaining items, and prints counts by status. It returns
// after the in-flight item commits when ctx is cancelled, so SIGINT/SIGTERM
// leave only pending, done or failed rows.
func (r *Runner) Run(ctx context.Context, opts RunOptions) (Stats, error) {
	if opts.Concurrency == 0 {
		opts.Concurrency = 1
	}
	if opts.Concurrency < 1 || opts.Concurrency > 2 {
		return Stats{}, fmt.Errorf("concurrency must be 1 or 2, got %d", opts.Concurrency)
	}
	out := r.out()
	// A signal cancels ctx; DB writes and the in-flight request must not be
	// cancelled by it (parse.go uses the same pattern).
	dbCtx := context.WithoutCancel(ctx)

	rows, err := r.eligible(dbCtx)
	if err != nil {
		return Stats{}, err
	}
	st := Stats{Eligible: len(rows)}

	if opts.Sample > 0 {
		ids := Sample(sampleCandidates(rows), opts.Sample, opts.Seed)
		fmt.Fprintf(out, "sample %d (seed %d): %d ids\n", opts.Sample, opts.Seed, len(ids))
		for _, id := range ids {
			fmt.Fprintln(out, id)
		}
		if opts.PrintSample {
			st.Selected = len(ids)
			return st, nil
		}
		rows = filterByID(rows, ids)
	}

	// The limit caps the selection by symbol id before skipping, so rerunning
	// the same command re-picks the same rows and skips the ones already done.
	slices.SortFunc(rows, func(a, b symbolRow) int { return cmp.Compare(a.id, b.id) })
	if opts.Limit > 0 && len(rows) > opts.Limit {
		rows = rows[:opts.Limit]
	}
	st.Selected = len(rows)

	work, skipped, err := r.work(rows, opts.RetryFailed)
	if err != nil {
		return st, err
	}
	st.Skipped = skipped

	done, failed, err := r.process(ctx, dbCtx, work, opts.Concurrency)
	st.Done, st.Failed = done, failed
	if err != nil {
		return st, err
	}
	counts, err := r.statusCounts(dbCtx)
	if err != nil {
		return st, err
	}
	st.Pending = counts["pending"]
	fmt.Fprintf(out, "eligible %d, selected %d, skipped %d, done %d, failed %d, pending %d\n",
		st.Eligible, st.Selected, st.Skipped, st.Done, st.Failed, st.Pending)
	return st, nil
}

// eligible returns every in-slice function or method whose body is at least
// [MinBodyLines] lines, with its current function-summary status and hash, ordered by
// qualified_name (the sampling order).
func (r *Runner) eligible(ctx context.Context) ([]symbolRow, error) {
	rows, err := r.Pool.Query(ctx, `
		SELECT s.id, s.qualified_name, f.path, s.kind, f.in_slice,
		       s.start_line, s.end_line, coalesce(s.signature, ''),
		       coalesce(s.doc, ''), s.raw_calls,
		       coalesce(sm.status, ''), coalesce(sm.input_hash, '')
		FROM symbols s
		JOIN files f ON f.id = s.file_id
		LEFT JOIN summaries sm
		  ON sm.level = 'function' AND sm.target_id = s.id AND sm.prompt_version = $1
		WHERE f.in_slice
		ORDER BY s.qualified_name`, PromptVersion)
	if err != nil {
		return nil, fmt.Errorf("list eligible symbols: %w", err)
	}
	defer rows.Close()

	var out []symbolRow
	for rows.Next() {
		var sr symbolRow
		var kind string
		var inSlice bool
		if err := rows.Scan(&sr.id, &sr.qualifiedName, &sr.path, &kind, &inSlice,
			&sr.sym.StartLine, &sr.sym.EndLine, &sr.sym.Signature,
			&sr.sym.Doc, &sr.sym.RawCalls, &sr.storedStatus, &sr.storedHash); err != nil {
			return nil, fmt.Errorf("scan symbol: %w", err)
		}
		sr.sym.Kind = kind
		sr.sym.QualifiedName = sr.qualifiedName
		if !Eligible(sr.sym, inSlice) {
			continue
		}
		out = append(out, sr)
	}
	return out, rows.Err()
}

// work builds the prompt input and input hash for each row and returns the rows
// that still need a summary, plus the number skipped because they are already
// done with the same input hash or failed without --retry-failed (D-065).
func (r *Runner) work(rows []symbolRow, retryFailed bool) ([]workItem, int, error) {
	work := make([]workItem, 0, len(rows))
	skipped := 0
	for _, sr := range rows {
		in, err := BuildInput(r.KubeDir, sr.path, sr.sym)
		if err != nil {
			return nil, 0, fmt.Errorf("read %s: %w", sr.path, err)
		}
		hash := in.Hash()
		if sr.storedStatus == "done" && sr.storedHash == hash {
			skipped++
			continue
		}
		if sr.storedStatus == "failed" && !retryFailed {
			skipped++
			continue
		}
		work = append(work, workItem{row: sr, input: in, hash: hash})
	}
	return work, skipped, nil
}

// process summarizes the work items with at most concurrency workers. It stops
// starting new items once ctx is cancelled or a worker reports an error, then
// waits for the in-flight items to commit.
func (r *Runner) process(ctx context.Context, dbCtx context.Context, work []workItem, concurrency int) (int, int, error) {
	var (
		mu       sync.Mutex
		firstErr error
		stopped  atomic.Bool
		done     int
		failed   int
	)
	fail := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if firstErr == nil {
			firstErr = err
		}
		stopped.Store(true)
	}
	record := func(status string) {
		mu.Lock()
		defer mu.Unlock()
		if status == "done" {
			done++
		} else {
			failed++
		}
	}

	jobs := make(chan workItem)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range jobs {
				if ctx.Err() != nil || stopped.Load() {
					continue // drain after an interrupt without doing more work
				}
				status, err := r.processItem(dbCtx, it)
				if err != nil {
					fail(err)
					continue
				}
				record(status)
			}
		}()
	}

produce:
	for _, it := range work {
		if stopped.Load() {
			break
		}
		select {
		case jobs <- it:
		case <-ctx.Done():
			break produce
		}
	}
	close(jobs)
	wg.Wait()
	return done, failed, firstErr
}

// processItem writes the row pending in its own transaction, sends the request
// (with the non-cancelled context so an interrupt finishes the in-flight item)
// and finalizes the row done or failed in a second transaction.
func (r *Runner) processItem(ctx context.Context, it workItem) (string, error) {
	if err := r.markPending(ctx, it); err != nil {
		return "", err
	}
	res := r.Client.Summarize(ctx, SystemPrompt, it.input.UserMessage())
	status, reason, text := "done", "", res.Text
	if !res.OK() {
		status, reason, text = "failed", res.Reason, ""
	}
	if err := r.finalize(ctx, it.row.id, status, text, reason); err != nil {
		return "", err
	}
	return status, nil
}

// markPending upserts the row on summaries_uniq with status pending, so a crash
// or interrupt between here and finalize leaves a pending (not partial) row.
func (r *Runner) markPending(ctx context.Context, it workItem) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO summaries (level, target_id, text, model, prompt_version, input_hash, status, reason)
		VALUES ('function', $1, NULL, $2, $3, $4, 'pending', NULL)
		ON CONFLICT (level, target_id, prompt_version) DO UPDATE SET
			status = 'pending', text = NULL, reason = NULL,
			model = EXCLUDED.model, input_hash = EXCLUDED.input_hash`,
		it.row.id, r.Model, PromptVersion, it.hash); err != nil {
		return fmt.Errorf("mark pending %d: %w", it.row.id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit pending: %w", err)
	}
	return nil
}

// finalize sets the row's terminal status, text and reason in one transaction.
func (r *Runner) finalize(ctx context.Context, id int64, status, text, reason string) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		UPDATE summaries SET status = $1, text = $2, reason = $3
		WHERE level = 'function' AND target_id = $4 AND prompt_version = $5`,
		status, store.NullIfEmpty(text), store.NullIfEmpty(reason), id, PromptVersion); err != nil {
		return fmt.Errorf("finalize %d: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit finalize: %w", err)
	}
	return nil
}

// statusCounts returns the number of current-version function rows per status.
func (r *Runner) statusCounts(ctx context.Context) (map[string]int, error) {
	rows, err := r.Pool.Query(ctx, `
		SELECT status, count(*) FROM summaries
		WHERE level = 'function' AND prompt_version = $1
		GROUP BY status`, PromptVersion)
	if err != nil {
		return nil, fmt.Errorf("count summaries: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int, 3)
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("scan count: %w", err)
		}
		counts[status] = n
	}
	return counts, rows.Err()
}

func (r *Runner) out() io.Writer {
	if r.Out == nil {
		return io.Discard
	}
	return r.Out
}

// Candidate is one eligible function offered to [Sample]: its symbol id, its
// qualified name (the deterministic ordering key) and its slice root.
type Candidate struct {
	ID            int64
	QualifiedName string
	Root          string
}

// Sample selects at most n candidate ids deterministically. Candidates are
// grouped by Root in walk.SliceRoots order; the n slots are allocated across
// the roots in proportion to each root's eligible count by the largest-
// remainder (Hare quota) method; within a root the candidates, sorted by
// QualifiedName, are shuffled with a math/rand generator seeded by seed and the
// first allocation are taken. The same (candidates, n, seed) always returns the
// same ids in the same order. The result is smaller than n only when fewer
// candidates exist.
func Sample(cands []Candidate, n int, seed int64) []int64 {
	if n <= 0 || len(cands) == 0 {
		return nil
	}
	ordered := slices.Clone(cands)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].QualifiedName < ordered[j].QualifiedName
	})
	if n >= len(ordered) {
		ids := make([]int64, len(ordered))
		for i, c := range ordered {
			ids[i] = c.ID
		}
		return ids
	}

	roots := rootOrder(ordered)
	byRoot := make(map[string][]Candidate, len(roots))
	for _, c := range ordered {
		byRoot[c.Root] = append(byRoot[c.Root], c)
	}
	quotas := allocate(n, byRoot, roots)

	groups := make([][]Candidate, len(roots))
	qs := make([]int, len(roots))
	for i, root := range roots {
		groups[i], qs[i] = byRoot[root], quotas[root]
	}
	var ids []int64
	for _, c := range seeded.Stratified(groups, qs, seed) {
		ids = append(ids, c.ID)
	}
	return ids
}

// allocate distributes n slots over the strata in proportion to their sizes by
// the largest-remainder method: each root gets floor(n*size/total) slots, and
// the leftover slots go to the roots with the largest remainders, ties broken
// by roots order.
func allocate(n int, byRoot map[string][]Candidate, roots []string) map[string]int {
	sizes := make(map[string]int, len(roots))
	for _, root := range roots {
		sizes[root] = len(byRoot[root])
	}
	return allocateCounts(n, sizes, roots)
}

// allocateCounts distributes n slots over the keys in order in proportion to
// their sizes by the largest-remainder (Hare quota) method: each key gets
// floor(n*size/total) slots, and the leftover slots go to the keys with the
// largest remainders, ties broken by order. Every size is assumed present in
// sizes; a zero total returns empty quotas.
func allocateCounts(n int, sizes map[string]int, order []string) map[string]int {
	total := 0
	for _, key := range order {
		total += sizes[key]
	}
	quotas := make(map[string]int, len(order))
	if total == 0 {
		return quotas
	}
	type remainder struct {
		key   string
		value int
		rank  int
	}
	rems := make([]remainder, 0, len(order))
	assigned := 0
	for i, key := range order {
		size := sizes[key]
		q := n * size / total
		quotas[key] = q
		assigned += q
		rems = append(rems, remainder{key: key, value: n*size - q*total, rank: i})
	}
	sort.SliceStable(rems, func(i, j int) bool {
		if rems[i].value != rems[j].value {
			return rems[i].value > rems[j].value
		}
		return rems[i].rank < rems[j].rank
	})
	for i := 0; i < n-assigned && i < len(rems); i++ {
		quotas[rems[i].key]++
	}
	return quotas
}

// rootOrder returns the roots present in ordered, walk.SliceRoots first in
// their configured order, then any other roots (there should be none) sorted by
// name so the result is independent of map iteration.
func rootOrder(ordered []Candidate) []string {
	seen := make(map[string]bool)
	for _, c := range ordered {
		seen[c.Root] = true
	}
	var roots []string
	sliceRoots := walk.SliceRoots()
	known := make(map[string]bool, len(sliceRoots))
	for _, p := range sliceRoots {
		known[p] = true
		if seen[p] {
			roots = append(roots, p)
		}
	}
	var extra []string
	for root := range seen {
		if !known[root] {
			extra = append(extra, root)
		}
	}
	sort.Strings(extra)
	return append(roots, extra...)
}

// sampleCandidates maps eligible rows to [Candidate], assigning each its slice
// root.
func sampleCandidates(rows []symbolRow) []Candidate {
	out := make([]Candidate, len(rows))
	for i, sr := range rows {
		out[i] = Candidate{ID: sr.id, QualifiedName: sr.qualifiedName, Root: walk.SliceRootOf(sr.path)}
	}
	return out
}

// filterByID keeps the rows whose id is in ids.
func filterByID(rows []symbolRow, ids []int64) []symbolRow {
	set := make(map[int64]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	out := make([]symbolRow, 0, len(ids))
	for _, sr := range rows {
		if set[sr.id] {
			out = append(out, sr)
		}
	}
	return out
}
