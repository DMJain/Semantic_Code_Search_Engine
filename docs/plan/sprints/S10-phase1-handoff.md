# S10 - Phase 1 handoff (gate)

## Goal
Document everything Phase 1 built and how to verify it from a clean machine, then STOP and wait for the author to test. No Phase 2 work starts in this sprint or before explicit approval.

## Entry criteria
S0 to S9 done (or consciously dropped per the Phase 1 priority list in ROADMAP.md, with the drops recorded in the sprint Results).

## Links
[eval-protocol.md#reproducibility](../eval-protocol.md#reproducibility), [#limitations-to-be-repeated-in-readme](../eval-protocol.md#limitations-to-be-repeated-in-readme); decisions D-010, D-046.

## Tasks

### S10-T01 - Write `docs/plan/phase1-handoff.md`
- Why: the author tests Phase 1 from this document alone.
- Depends on: none
- Touches: `docs/plan/phase1-handoff.md` (new)
- Details: sections in this order: (1) What was built: one line per sprint with the main commands. (2) Run from a clean machine: exact commands in order (`brew`/prerequisites, `.env` creation, `make up`, `make migrate`, `make doctor`, `make fetch-repo`, each `indexer` stage with flags, `make summarize-run`, `go run ./cmd/server`, `go run ./cmd/eval ...`), each with its expected output in one line. (3) Test checklist: a table of `Feature | Command | Expected outcome` covering every feature: walk exclusions, parse, graph, callgraph-gt, chunk, embed, A0/A1/A2 search CLI, `/search` for each route, `/ask` both modes, `/trace/{id}`, trace UI pages, `eval validate --strict`, `eval freeze --verify`, `eval report`, ANN overlap, Q&A sheet leak check, CI fixture status. (4) Results summary: headline numbers copied by link and reference to `eval/report.md`, including the configurations that do not beat the baseline. (5) Known limitations and open issues: from `eval-protocol.md` limitations plus anything found in sprint Results. No time estimates.
- Acceptance: every command in section 2 and 3 appears in the Makefile, `--help` output of a binary, or a `go run` path that exists (`scripts/check-handoff-commands.sh` extracts the backticked commands and exits 0); the document has exactly the five sections above; every checklist row has a non-empty expected outcome.
- Tests: the script is the test.
- Out of scope: Phase 2 content.
- Owner: agent

### S10-T02 - Clean-checkout verification of the cheap checklist rows
- Why: the handoff must not describe commands that fail from a fresh clone.
- Depends on: S10-T01
- Touches: this file (Results)
- Details: clone the repo into a scratch directory, `make reset-db`, run every checklist row that does not need the full Kubernetes index (migrate, doctor, doctor --probe-all, walk/parse with `--limit`, `eval validate --strict`, `eval freeze --verify`, `eval report`, `go test -count=1 ./...` with `TEST_DATABASE_URL`, lint). Record pass/fail per row. Fix documentation errors in the handoff; fix real bugs only by opening a ticket and finishing it before returning here.
- Acceptance: every cheap row passes from the fresh clone and Results holds the table; the rows that need the full index are marked `requires full index` with the reason.
- Tests: none.
- Out of scope: re-indexing Kubernetes.
- Owner: agent

### S10-T03 - GATE: stop and hand over
- Why: Phase 2 begins only after the author approves Phase 1.
- Depends on: S10-T02
- Touches: ROADMAP.md (status), `docs/plan/decisions.md` (D-046 stays PENDING)
- Details: set ROADMAP status for S0-S10 to done. Print exactly this message to the author and then take NO further action (no Phase 2 files, no P2 tickets, no refactors): `Phase 1 is complete and ready for you to test. See docs/plan/phase1-handoff.md.` Do not mark D-046 approved. When, in a later message, the author explicitly approves Phase 1, record the approval (date, the author's words) in D-046; only then may P2-S0 start. If the author reports failures, fix them as new tickets inside Phase 1.
- Acceptance: `grep -n "PENDING" docs/plan/decisions.md` still lists D-046 at the end of this task; no file under `.scratch/P2-*` exists; the agent's final message contains the sentence above.
- Tests: none.
- Out of scope: everything in Phase 2.
- Owner: agent

## Measurements to record
None new; the handoff cites existing results by path.

## Exit criteria
`phase1-handoff.md` exists and passes its command check; the cheap checklist passes from a fresh clone; the gate message was given; D-046 is PENDING until the author approves.

## Risks + mitigations
- Documentation drifts from the code: the command-check script and the fresh-clone run catch it.
- An agent continues into Phase 2 on its own: S10-T03 acceptance checks that no Phase 2 artifacts exist.

## Demo check
Open `docs/plan/phase1-handoff.md` and run the first three checklist rows.

## Results
(empty)
