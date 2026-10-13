# 01: VTA memory measurement and load invocation

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 1-13, 25, 100, 97, 99.

**What to build:** The ground-truth tool can load the frozen slice, build SSA and the VTA call graph over the slice functions only, and report what that cost, without touching the database. The run records the working load invocation (workspace load; the vendor load is known to fail, D-071), closes Q-04 with a measured peak memory, and closes Q-05 only if the load also works with the network off. This is the only S2 step that can need an owner decision, so it goes first. The SSA/VTA build step it creates is the one ticket 10 extends to store edges; it is not a throwaway.

Before the measurement: stop the model server, pause the summary run with the documented safe pause, run no embedding job and no other session on the machine, and set a memory limit for the Go runtime. State in the Report that this was done.

If peak resident memory is above 16 GiB: write and commit an escalation report (which step ran out of memory, the peak, what was tried), set this ticket's Status to ready-for-human instead of done, and stop. Do not adopt per-root runs, CHA or RTA: they change the ground truth (D-011) and need the owner. Tickets 02 to 09 do not depend on this one and may continue; ticket 10 and later stay blocked until this ticket is resolved.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] Load environment is `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=local` under the workspace load; a unit test covers the environment and the shape of the result metadata
- [ ] `go run ./cmd/callgraph-gt --check` still exits 0 and prints `file-set: equal (876 files)` after the load settings change
- [ ] A read-only run under `/usr/bin/time -l` with a memory limit exits 0 and writes result metadata containing `peak_rss_bytes`, packages loaded, SSA function count, VTA edge total, in-slice edge count and wall time; `select count(*) from edges` is unchanged by the run
- [ ] The VTA input is the slice functions plus the initial call graph only
- [ ] A load with `GOPROXY=off` and `GOFLAGS=-mod=readonly` is attempted and its outcome recorded
- [ ] D-025's working invocation is filled (no PENDING left on that line); the Q-04 entry is deleted with the measured peak in the Report; the Q-05 entry is deleted only if the offline load succeeded, otherwise it stays with the reason
- [ ] If peak > 16 GiB: escalation report committed and Status is ready-for-human (the other criteria are then not required)
- [ ] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [ ] One commit `S2-01: ...` that also sets this ticket's Status to done
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Storing any edge (ticket 10); the report; per-root runs, CHA, RTA or any other fallback; changing the slice.
