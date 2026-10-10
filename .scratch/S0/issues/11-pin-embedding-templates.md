# 11: Pin the embedding templates (S0 gap)

**What to build:** A change to any embedding prompt template is caught by a test, not only by a reader. The rendered-template hashes recorded in D-003 become test expectations through the public embedder, and a one-character change to each of the four templates is shown to make a test fail (evidence recorded). The probe's unit-norm acceptance range tightens from plus or minus 5% to plus or minus 1%, since measured norms are 1.0000. The Qwen3 query format choice (no space after "Query:", following the model card's own helper rather than its curl example) is recorded as an outcome under D-003.

**Blocked by:** None (can start immediately)

**Status:** done

**Found by:** S0 code review (hash-pin, mutation demonstration, norm bounds, Qwen3 prefix record).

- [ ] A test fails when any of the six rendered strings (query, document, document with title, for each model) differs by one byte from the hash in D-003
- [ ] Mutation evidence: one-character edit of each of the four templates makes `go test ./internal/embed/...` fail; the edits are reverted; transcript recorded in the S0 sprint Results
- [ ] Probe norm range is 0.99 to 1.01; `doctor --probe-all` still PASS for both models
- [ ] D-003 has an outcome line recording the Qwen3 format choice and the evidence that the card's curl example differs
- [ ] `go vet ./...`, `make lint`, `make test` (0 SKIP) green; one commit

Out of scope: choosing between the two Qwen3 formats by retrieval quality (evaluation sprint).
