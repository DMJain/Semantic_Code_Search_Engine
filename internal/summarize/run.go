package summarize

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/store"
)

// Runner summarizes eligible in-slice functions into the summaries table. It is
// the database-backed half of the summarize stage: each item is
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
	st.Pending = counts[StatusPending.String()]
	fmt.Fprintf(out, "eligible %d, selected %d, skipped %d, done %d, failed %d, pending %d\n",
		st.Eligible, st.Selected, st.Skipped, st.Done, st.Failed, st.Pending)
	return st, nil
}

// work builds the prompt input and input hash for each row and returns the rows
// that still need a summary, plus the number skipped because they are already
// current (see [symbolRow.skip], D-065).
func (r *Runner) work(rows []symbolRow, retryFailed bool) ([]workItem, int, error) {
	work := make([]workItem, 0, len(rows))
	skipped := 0
	for _, sr := range rows {
		in, err := BuildInput(r.KubeDir, sr.path, sr.sym)
		if err != nil {
			return nil, 0, fmt.Errorf("read %s: %w", sr.path, err)
		}
		hash := in.Hash()
		if sr.skip(hash, retryFailed) {
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
	record := func(status Status) {
		mu.Lock()
		defer mu.Unlock()
		if status == StatusDone {
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
func (r *Runner) processItem(ctx context.Context, it workItem) (Status, error) {
	if err := r.markPending(ctx, it); err != nil {
		return "", err
	}
	res := r.Client.Summarize(ctx, SystemPrompt, it.input.UserMessage())
	status, reason, text := StatusDone, "", res.Text
	if !res.OK() {
		status, reason, text = StatusFailed, res.Reason, ""
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
		VALUES ($5, $1, NULL, $2, $3, $4, $6, NULL)
		ON CONFLICT (level, target_id, prompt_version) DO UPDATE SET
			status = $6, text = NULL, reason = NULL, completed_at = NULL,
			model = EXCLUDED.model, input_hash = EXCLUDED.input_hash`,
		it.row.id, r.Model, PromptVersion, it.hash, LevelFunction, StatusPending); err != nil {
		return fmt.Errorf("mark pending %d: %w", it.row.id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit pending: %w", err)
	}
	return nil
}

// finalize sets the row's terminal status, text and reason in one transaction.
func (r *Runner) finalize(ctx context.Context, id int64, status Status, text, reason string) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		UPDATE summaries SET status = $1, text = $2, reason = $3,
			completed_at = CASE WHEN $1 = $7 THEN now() END
		WHERE level = $6 AND target_id = $4 AND prompt_version = $5`,
		status, store.NullIfEmpty(text), store.NullIfEmpty(reason), id, PromptVersion, LevelFunction, StatusDone); err != nil {
		return fmt.Errorf("finalize %d: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit finalize: %w", err)
	}
	return nil
}

func (r *Runner) out() io.Writer {
	if r.Out == nil {
		return io.Discard
	}
	return r.Out
}
