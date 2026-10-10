# 04: Verified embedding prefixes, third model dropped (S0-T05, decision D-003)

**What to build:** The embedding registry contains exactly two models, the Qwen3 0.6B embedder and nomic-embed-text, and their query and document prompt templates are verified against the official model cards rather than assumed. The dropped third model disappears from code, tests, config examples and doctor text. Verification is evidence-first: primary source is the upstream Hugging Face card for each model, secondary is the Ollama library page.

Verify, do not assume:
- Qwen3 query format is `Instruct: {task}\nQuery:{query}` with unprefixed documents; pin ONE fixed task sentence. The current template has a space after "Query:" that may not match the card; the card decides.
- nomic uses `search_query: ` and `search_document: `.
- State one rule for how a document title joins its text, consistent with the embed stage in the architecture doc, and record it.

For each model collect: card URL, retrieval date, quoted prefixes, native dimension, Matryoshka yes/no, maximum context, embedding context size (4096 unless the card maximum is lower; if lower, the chunk limit drops and that is recorded).

Decision log, append-only: add under D-003 an `Outcome (S0-T05, date, evidence)` line with the above plus the sha256 of each rendered template for fixed sample inputs. If any string differs from what the plan assumed, add a new decision that supersedes that part of D-003 and mark D-003 "Superseded by" for that part; never edit D-003's existing text.

**Blocked by:** 01 (Strict, uncached `make test`)

**Status:** ready-for-agent

**Spec user stories:** 15, 16, 17, 18, 19, 20, 21, 22, 23

- [ ] A test asserts the known-model list is exactly `nomic-embed-text` and `qwen3-embedding:0.6b`
- [ ] Table-driven tests drive the public embedding interface against a fake Ollama and assert the exact request-body bytes (whitespace and newlines included) for a query and a document, with and without a title, per model
- [ ] A one-character mutation of any template makes a test fail (demonstrated once with a temporary mutation, then reverted)
- [ ] `! grep -rniI 'g[e]mma' . --exclude-dir=.git --exclude-dir=plan --exclude-dir=.scratch` exits 0
- [ ] With Ollama up, `make doctor` exits 0 and the probe prints query and doc norms of exactly `1.0000`
- [ ] D-003 outcome line (and any superseding decision) recorded with URL, date, quoted strings, dim, MRL, context, embedding context size, hashes
- [ ] `make test`, `go vet ./...`, `make lint` clean; one commit `S0-T05: ...`; nothing pushed
- [ ] Report: evidence table per model, hashes, any supersession, probe output, commit hash

Out of scope: the live probe-all (ticket 06).
