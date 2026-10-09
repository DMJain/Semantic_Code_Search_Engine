# P2-S10 - Phase 2 handoff (gate)

## Goal
Document everything Phase 2 built and how to verify it from a clean machine, then STOP and wait for the author to test.

## Entry criteria
P2-S0 to P2-S9 done (or consciously dropped, with the drops recorded in sprint Results). D-046 (Phase 1 approval) is recorded.

## Links
[v2-architecture.md#phase-1-protection](../v2-architecture.md#phase-1-protection), [#distribution](../v2-architecture.md#distribution); [eval-protocol.md#phase-2-parity](../eval-protocol.md#phase-2-parity); decisions D-040, D-047.

## Tasks

### P2-S10-T01 - Write `docs/plan/phase2-handoff.md`
- Why: the author tests Phase 2 from this document alone.
- Depends on: none
- Touches: `docs/plan/phase2-handoff.md` (new)
- Details: sections in order: (1) What was built: one line per Phase 2 sprint with the main commands. (2) Install from a clean machine: the exact commands for each path (brew, install script, `go install`) with the expected output line. (3) Test checklist across several repos: a table `Repo | Command | Expected outcome` covering `index` (first and incremental), `search`, `find`, `callers`, `callees`, follow-ups with `--session`, `chat` with and without Ollama, `mcp` via `connect` in Claude Code and Cursor, `status`, `doctor`, `uninstall --dry-run`, on at least these repos: the fixture repo, Kubernetes (parity config), and the three smoke repos (one TS, one Python, one Java), plus one repo in a tier-0 language. (4) Results summary: links to the Phase 2 section of `eval/report.md` and `eval/results/phase2/`, including the results that are worse than Phase 1 and the resource numbers with their measurement method. (5) Known limitations and open issues. No time estimates.
- Acceptance: `scripts/check-handoff-commands.sh docs/plan/phase2-handoff.md` (the Phase 1 script, parameterized) exits 0; the document has exactly the five sections; every checklist row has a non-empty expected outcome.
- Tests: the script is the test.
- Out of scope: new features.
- Owner: agent

### P2-S10-T02 - Clean-machine install and checklist run
- Why: install must work for someone who is not the developer.
- Depends on: P2-S10-T01
- Touches: this file (Results)
- Details: on a clean environment (a new macOS user account or a Linux amd64 and a Linux arm64 container) run install path 2 of the handoff, then every checklist row that does not need the full Kubernetes index (fixture repo, one smoke repo). Record pass/fail per row. Fix documentation errors in the handoff; fix real bugs only by opening a ticket and finishing it first.
- Acceptance: Results holds the table with each row passing from the clean environment; CI is green on the commit; `lodestone uninstall --dry-run` lists only the expected paths.
- Tests: none.
- Out of scope: re-running the evaluation.
- Owner: agent

### P2-S10-T03 - GATE: stop and hand over
- Why: the author tests Phase 2 before it is considered done.
- Depends on: P2-S10-T02
- Touches: ROADMAP.md (status), `docs/plan/decisions.md` (D-047 stays PENDING)
- Details: set ROADMAP status for P2-S0 to P2-S10 to done. Print exactly: `Phase 2 is complete and ready for you to test. See docs/plan/phase2-handoff.md.` and take NO further action. Do not mark D-047 approved. Record the author's approval in D-047 only after an explicit later message; failures the author reports become new tickets inside Phase 2.
- Acceptance: `grep -n "PENDING" docs/plan/decisions.md` still lists D-047; the agent's final message contains the sentence above.
- Tests: none.
- Out of scope: any new work.
- Owner: agent

## Measurements to record
None new; the handoff cites existing results by path.

## Exit criteria
`phase2-handoff.md` exists and passes its command check; the clean-environment run passes; the gate message was given; D-047 is PENDING until the author approves.

## Risks + mitigations
- Install works only on the developer's machine: the clean-environment task exists for that reason.
- An agent continues past the gate: T03 acceptance checks the message and the PENDING marker.

## Demo check
Open `docs/plan/phase2-handoff.md`, install with the script and run the first three checklist rows.

## Results
(empty)
