package summarize

import (
	"context"
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
// pending rows at the measured rate and is zero (Measured false) when the
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
// wall-clock span, and derives an ETA for the pending rows.
func (r *Runner) Status(ctx context.Context, window time.Duration) (StatusReport, error) {
	if window <= 0 {
		window = DefaultStatusWindow
	}
	before, err := r.statusCounts(ctx)
	if err != nil {
		return StatusReport{}, err
	}
	// Nothing pending means there is nothing to estimate; report at once.
	if before["pending"] == 0 {
		return statusReport(before, before, window), nil
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
	return statusReport(before, after, window), nil
}

// statusReport derives the report from two done-count samples; it is pure so
// the ETA arithmetic is testable without waiting on a real window.
func statusReport(before, after map[string]int, window time.Duration) StatusReport {
	rep := StatusReport{
		PromptVersion: PromptVersion,
		Counts:        after,
		Remaining:     after["pending"],
		Window:        window,
	}
	delta := after["done"] - before["done"]
	if delta <= 0 || window <= 0 {
		return rep
	}
	rate := float64(delta) / window.Seconds()
	rep.Measured = true
	rep.PerHour = rate * 3600
	rep.ETA = time.Duration(float64(rep.Remaining) / rate * float64(time.Second))
	return rep
}
