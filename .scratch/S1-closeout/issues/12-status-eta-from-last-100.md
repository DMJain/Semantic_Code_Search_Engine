# 12: Status ETA from the last 100 completions

Plan: S1 close-out spec, stories 8, 9, 10, 11, 12, 49. Review finding: the ETA is a live 30-second sample, not "an estimate from the last 100 completions"; no ETA when nothing is pending at the first reading; the summaries table has no timestamp.

**What to build:** A reversible migration adds a nullable completion timestamp to summaries, set when a row becomes done. Status counts only the current prompt version and computes the rate from the most recent 100 timestamped completions of that version, then ETA from the whole remaining queue. With fewer than two timestamped completions it prints "ETA unknown" and the reason. Status returns immediately (no sleep). Rows already done have no timestamp and are simply not used for the rate. The migration applies cleanly on the database that holds the overnight run's rows.

**Blocked by:** 09

**Status:** ready-for-agent

- [ ] A test with a fake clock and seeded timestamps: the ETA follows the last 100 completions, not older ones
- [ ] A test: fewer than two timestamped completions prints "ETA unknown" with the reason
- [ ] A test: rows of another prompt version are ignored
- [ ] Migration up, down, up passes; applying it to a copy of the real database keeps all rows
- [ ] `make summarize-status` returns without a sampling delay
- [ ] One commit `S1C-12: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Backfilling timestamps for rows already done.

## Comments

Migration evidence (2026-10-13). On `CREATE DATABASE zz_s1c12_copy TEMPLATE codesearch` (a copy of the real database holding 6694 done rows, goose version 3), driven through `go run ./cmd/indexer migrate` / `migrate down` with `DATABASE_URL` pointed at the copy:

- up: `applied migration version=4`; 6694 rows, 0 with `completed_at`
- down: `rolled back migration version=4`; 6694 rows, goose version 3
- up again: applied; 6694 rows, goose version 4

The copy was then dropped. The real database was migrated with `make migrate` (`applied migration version=4`; 6694 rows kept, 0 timestamped). `make summarize-status` on it returned in 0.25s: `pending 0 done 6694 failed 0`, `remaining 0`, `eta 0s (nothing pending)`.
