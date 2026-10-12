# summarize --status: remaining and ETA only count in-flight rows

Status: resolved

## Observed
`make summarize-status` during the fn-v1 run prints `pending 1`, `remaining 1`,
`eta 6s` while about 6,300 functions are still unsummarized.

## Cause (verified)
- `internal/summarize/status.go:63` sets `Remaining: after["pending"]`.
- Rows are only written `pending` one at a time by `markPending`
  (`internal/summarize/run.go:302`) when an item is picked up, so with
  `--concurrency 1` the pending count is never above 1.
- The ETA (`status.go:73`) divides that count by the measured rate, so it is
  always a few seconds.
- The 30s rate (`PerHour`) is correct but noisy.

## Fix
Define remaining as eligible minus done minus failed (failed only if
`--retry-failed` is not in play), computed in `Status`:
- Reuse `r.eligible(ctx)` (the same query `Run` uses) to get the eligible
  count for the current prompt version, and subtract the `done` count from
  `statusCounts`.
- Set `Remaining` and the ETA from that. Keep `pending` printed as in-flight.
- Add a unit test on `statusReport` with eligible 6673, done 379, rate 600/h,
  expecting remaining 6294 and an ETA of about 10.5h. Cover the
  `delta <= 0` case (no ETA).
- Consider a longer default window or a rate averaged since run start, since
  30s windows swing between 600 and 960 fn/h.

## Not blocking
S1 "done" only needs `done` to grow; do not change this before the run ends,
since rebuilding `bin/indexer` mid-run is harmless but unnecessary.

## Resolution
Fixed in `internal/summarize/status.go`. `Status` now counts eligible rows with
`r.eligible(ctx)` and `statusReport` sets `Remaining = max(0, eligible - done -
failed)`, subtracting failed unless the status call is a `--retry-failed` run.
The CLI passes the existing `--retry-failed` flag through. `pending` stays
printed as in-flight. Tests: `TestStatusReportETA` (eligible 6673, done 379,
600 fn/h -> remaining 6294, ETA ~10.5h), `TestStatusReportFailedRemaining`, and
`TestStatusReportNoProgress` (delta == 0 and delta < 0). `bin/indexer` was not
rebuilt, so the live run is untouched. The optional longer-window/run-start rate
(L28-29) was left as-is.
