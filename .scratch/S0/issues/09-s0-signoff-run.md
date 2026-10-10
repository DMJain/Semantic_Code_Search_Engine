# 09: S0 sign-off run (S0-T10)

**What to build:** One recorded green run proving the S0 exit criteria, with CI evidence, and the sprint marked done.

Run in order: `make reset-db` (ticket 02's emptiness guard first), `go vet ./...`, `make lint`, `make test` (uncached, no SKIP), `make doctor`, `go run ./cmd/indexer doctor --probe-all`. Paste summaries into the sprint Results and confirm it holds: probe table, doctor transcripts A and B, Ollama IDs, a reference to the D-003 outcome, D-060 and D-061. Push the final commit to the branch (the approval from ticket 08 covers this branch only), confirm CI is green on that commit, and record the run URL. Update the S0 status in the roadmap and the Results header (from "in progress" to the sign-off date). Do not merge; tell Darshan the draft PR is ready for review.

**Blocked by:** 07 (Doctor live pass/fail), 08 (CI Postgres and draft PR), 05 (Docs housekeeping)

**Status:** ready-for-agent

**Spec user stories:** 39, 40, 41

- [ ] Every command above exits 0
- [ ] Results is filled with all the evidence listed above
- [ ] CI run URL recorded and green on the final commit
- [ ] D-003 outcome, D-060 and D-061 exist in the decision log
- [ ] Roadmap S0 status updated
- [ ] Report: command summaries, CI URL, PR URL

Out of scope: new features.
