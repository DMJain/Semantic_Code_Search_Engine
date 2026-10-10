# 16: Review cleanups (S0 code review)

**What to build:** The findings of the second S0 code review that are fixable without pushing are fixed, and the ones that are accepted are written down as accepted. The doctor code is split by mode and shares one model walker, one verdict helper and named constants for the token estimate; the dead probe wrapper is gone; CI runs `make test` as the single definition of the test step; changed decisions are recorded as a superseding decision; ticket bookkeeping matches reality.

**Blocked by:** 11, 12, 13, 14, 15

**Status:** done

**Found by:** `/code-review` of `main...s0-setup-verification` (Standards and Spec axes).

- [x] `ProbeOversizedInput` removed; its test now exercises `ProbeInput`
- [x] The ok/error and PASS/FAIL verdicts come from one helper; `worst` renamed `minNorm`
- [x] The create-embedder, check-pulled walk is one shared function used by `--probe-all` and `--ollama-checks`
- [x] The context-probe arithmetic uses named constants tied to D-015 (bytes per estimated token, 0.8 fraction, 1.5 factor)
- [x] The doctor modes live in separate files; flag sets are built by one shared helper
- [x] CI runs `make test` instead of repeating its logic
- [x] D-062 supersedes D-003's recorded nomic context size and records the Qwen3 format choice; D-003 points at it
- [x] Tickets 01 to 09 carry their real status; ticket 08 and 09 CI boxes stay unchecked and say why
- [x] Accepted, not changed (recorded in the S0 spec Further Notes): the `migrate-up` alias (spec story 7 asks for it), `migrate down` as a verb, `make test-db-clean`, `REQUIRE_TEST_DB`, `config.LoadOllama`, sending the context size, the tighter norm range, git being required by the fetch-repo tests
- [x] `go vet`, `make lint`, `make test` (0 SKIP) and the three doctor commands green; one commit

Out of scope: CI proof on a pushed branch (owner decision, D-061).
