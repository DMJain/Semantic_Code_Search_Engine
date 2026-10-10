# 12: Embedding context size is sent and measured (S0 gap)

**What to build:** Every embedding request carries an explicit context size from a per-model setting, and the doctor's Ollama checks measure, per pulled model, that an input within 80% of that size is accepted and an input well over it is rejected. This resolves the open caveat on the nomic context size. Live measurement on 2026-10-10 (recorded as evidence): nomic rejects inputs over about 2048 tokens even when asked for 4096 or 8192 (Ollama clamps to the model's 2048 context), while qwen3 honors the requested size and rejects about 4574 tokens at 4096. So the nomic setting is 2048, not 4096, and the architecture rule applies: the maximum estimated request size for nomic is 80% of 2048 (about 1638 tokens), which is below the current chunk ceiling. That consequence is recorded for S3 (S3 entry checklist), not fixed here.

**Blocked by:** None (can start immediately)

**Status:** done

**Found by:** S0 code review (nomic context-size caveat); the embed client never sent a context size although D-015 requires it.

- [ ] Per-model embedding context size lives in the registry (qwen3 4096, nomic 2048) and is exposed for the S3 pre-send guard
- [ ] Request-body tests assert the context size is in every embedding request body (query, document, probe)
- [ ] `doctor --ollama-checks` prints, per pulled model, a within-context check (expected ok) and an over-context check (expected error); it exits non-zero when either is unexpected; fake-Ollama tests cover both outcomes
- [ ] D-003 outcome: measured evidence table, nomic setting 2048, the 1638-token request ceiling consequence; the S3 entry checklist line is replaced by the concrete chunk-ceiling decision to make
- [ ] Live run transcript in the S0 sprint Results; `go vet`, `make lint`, `make test` (0 SKIP) green; one commit

Out of scope: changing chunk limits or the S3 chunker.
