package summarize

import (
	"context"
	"fmt"
	"time"
)

// DefaultStatusWindow is the wall-clock interval between the two done-count
// samples `summarize --status` uses to measure the current completion rate.
// The summaries table has no timestamp column and the migrations are frozen
// (D-070), so the rate is measured live over this window (the ticket's
// "wall-clock" method) instead of from stored completion times.
const DefaultStatusWindow = 30 * time.Second

// StatusReport is the summarize status for the current prompt version only
// (D-068). Counts holds pending/done/failed; ETA is the estimate to clear the
// remaining rows at the measured rate and is zero (Measured false) when the
// sampling window saw no completion.
type StatusReport struct {
	PromptVersion string
	Counts        map[string]int
	Remaining     int
	Window        time.Duration
	PerHour       float64
	ETA           time.Duration
	Measured      bool
}

// Status counts the current-version function summary rows by status, then
// counts again after window to measure how many rows completed in that
// wall-clock span, and derives an ETA for the rows that still need work.
// Remaining is eligible minus done (minus failed unless retryFailed, since a
// retry run re-picks failed rows), not just the in-flight pending count, so the
// ETA reflects the whole queue.
func (r *Runner) Status(ctx context.Context, window time.Duration, retryFailed bool) (StatusReport, error) {
	if window <= 0 {
		window = DefaultStatusWindow
	}
	// Count eligible rows so remaining reflects the whole queue, not just the
	// in-flight pending rows.
	rows, err := r.eligible(ctx)
	if err != nil {
		return StatusReport{}, err
	}
	eligible := len(rows)
	before, err := r.statusCounts(ctx)
	if err != nil {
		return StatusReport{}, err
	}
	// Nothing in flight means no rate can be measured; report at once.
	if before[StatusPending.String()] == 0 {
		return statusReport(eligible, before, before, retryFailed, window), nil
	}
	timer := time.NewTimer(window)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
	after, err := r.statusCounts(ctx)
	if err != nil {
		return StatusReport{}, err
	}
	return statusReport(eligible, before, after, retryFailed, window), nil
}

// statusReport derives the report from two done-count samples; it is pure so
// the ETA arithmetic is testable without waiting on a real window.
func statusReport(eligible int, before, after map[string]int, retryFailed bool, window time.Duration) StatusReport {
	failed := after[StatusFailed.String()]
	if retryFailed {
		failed = 0 // a retry run re-picks failed rows, so they are still remaining
	}
	rep := StatusReport{
		PromptVersion: PromptVersion,
		Counts:        after,
		Remaining:     max(0, eligible-after[StatusDone.String()]-failed),
		Window:        window,
	}
	delta := after[StatusDone.String()] - before[StatusDone.String()]
	if delta <= 0 || window <= 0 {
		return rep
	}
	rate := float64(delta) / window.Seconds()
	rep.Measured = true
	rep.PerHour = rate * 3600
	rep.ETA = time.Duration(float64(rep.Remaining) / rate * float64(time.Second))
	return rep
}

// statusCounts returns the number of current-version function rows per status.
func (r *Runner) statusCounts(ctx context.Context) (map[string]int, error) {
	rows, err := r.Pool.Query(ctx, `
		SELECT status, count(*) FROM summaries
		WHERE level = $2 AND prompt_version = $1
		GROUP BY status`, PromptVersion, LevelFunction)
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
