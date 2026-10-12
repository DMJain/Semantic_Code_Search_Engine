package summarize

import (
	"context"
	"fmt"
	"time"
)

// RateSamples is how many of the most recent timestamped completions of the
// current prompt version the ETA rate is computed from.
const RateSamples = 100

// minRateSamples is the fewest timestamped completions that give a rate (two
// completions bound one interval).
const minRateSamples = 2

// StatusReport is the summarize status for the current prompt version only
// (D-068). Counts holds pending/done/failed; ETA is the estimate to clear the
// whole remaining queue at the recent rate. When the rate cannot be computed
// Measured is false, ETA is zero and Reason says why.
type StatusReport struct {
	PromptVersion string
	Counts        map[string]int
	Remaining     int
	PerHour       float64
	ETA           time.Duration
	Measured      bool
	Reason        string
}

// Status counts the current-version function summary rows by status and
// derives an ETA from the stored completion times of the most recent
// [RateSamples] done rows. It never waits. Remaining is eligible minus done
// (minus failed unless retryFailed, since a retry run re-picks failed rows,
// or the failed row's input hash changed, since any run re-picks it),
// not just the in-flight pending count, so the ETA covers the whole queue.
func (r *Runner) Status(ctx context.Context, retryFailed bool) (StatusReport, error) {
	rows, err := r.eligible(ctx)
	if err != nil {
		return StatusReport{}, err
	}
	counts, err := r.statusCounts(ctx)
	if err != nil {
		return StatusReport{}, err
	}
	failed := counts[StatusFailed.String()]
	if retryFailed {
		failed = 0
	} else {
		// A failed row whose input changed is stale: the next run re-attempts
		// it, so it is remaining, not terminal.
		failed -= r.staleFailed(rows)
	}
	rep := StatusReport{
		PromptVersion: PromptVersion,
		Counts:        counts,
		Remaining:     max(0, len(rows)-counts[StatusDone.String()]-failed),
	}

	var n int
	var span float64
	if err := r.Pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(EXTRACT(EPOCH FROM max(completed_at) - min(completed_at)), 0)
		FROM (
			SELECT completed_at FROM summaries
			WHERE level = $1 AND prompt_version = $2 AND status = $3 AND completed_at IS NOT NULL
			ORDER BY completed_at DESC
			LIMIT $4) recent`,
		LevelFunction, PromptVersion, StatusDone, RateSamples).Scan(&n, &span); err != nil {
		return StatusReport{}, fmt.Errorf("read completion times: %w", err)
	}
	switch {
	case n < minRateSamples:
		rep.Reason = fmt.Sprintf("only %d timestamped completion(s) for %s, need at least %d (rows done before timestamps existed are not used)",
			n, PromptVersion, minRateSamples)
	case span <= 0:
		rep.Reason = fmt.Sprintf("the %d most recent timestamped completions share one instant", n)
	default:
		rate := float64(n-1) / span // completions per second
		rep.Measured = true
		rep.PerHour = rate * 3600
		rep.ETA = time.Duration(float64(rep.Remaining) / rate * float64(time.Second))
	}
	return rep, nil
}

// staleFailed counts the failed rows among rows that the next run would
// re-attempt without the retry flag, using the same currentness rule as the run
// ([symbolRow.skip]).
func (r *Runner) staleFailed(rows []symbolRow) int {
	n := 0
	for _, sr := range rows {
		if sr.storedStatus != StatusFailed {
			continue
		}
		in, err := BuildInput(r.KubeDir, sr.path, sr.sym)
		// An unreadable source cannot prove the row current, so it counts as
		// stale; the next run re-attempts it and reports the read error there.
		if err != nil || !sr.skip(in.Hash(), false) {
			n++
		}
	}
	return n
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
