# 09: Function summary prompt, Ollama call and request guards (S1-T09)

Plan: S1-T09 (`docs/plan/sprints/S1-corpus-symbols.md`)

**What to build:** Pure part of the summarizer: the `fn-v1` prompt, `summarize.PromptVersion` constant (single source), `input_hash`, selection (body of at least 3 lines), the Ollama chat call with explicit `num_ctx`, and the `too_large` (pre-send) and `truncated` (post-send) guards.

**Blocked by:** 06, 07

**Status:** ready-for-agent

**Spec user stories:** 25, 26

- [ ] `go test -count=1 ./internal/summarize/...` passes: golden prompt, stable `input_hash`, selection at 2 and 3 body lines, `num_ctx` in every request body, 0.81 x `num_ctx` not sent (fake server records 0 requests, `too_large`), `prompt_eval_count = num_ctx - num_predict` gives `truncated`
- [ ] `grep -rn '"fn-v' internal/ cmd/` shows only the constant
- [ ] One commit `S1-T09: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Database state and CLI (T10); file and package levels (S5).
