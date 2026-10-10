# 10: Summary prompt and request guards

Plan: S1-T09 (`docs/plan/sprints/S1-corpus-symbols.md`). Spec user stories: 38, 39, 40, 41.

**What to build:** The pure part of the summarizer exists: the `fn-v1` function prompt, a single current prompt-version constant, the input hash, the eligible-function selection (body of at least three lines), and the Ollama chat call with an explicit context size. A request estimated above 80% of the context is not sent and is reported `too_large`; a response whose prompt token count reaches the context minus the output limit is reported `truncated`.

**Blocked by:** 07 (Raw calls per function)

**Status:** ready-for-agent

- [ ] `go test -count=1 ./...` passes: golden prompt bytes, stable input hash, a two-line body excluded and a three-line body included, the context size present in every request body
- [ ] A request estimated at 81% of the context is not sent (the fake server records 0 requests) and returns `too_large`; a response at the limit returns `truncated`
- [ ] `grep -rn '"fn-v' .` over the source shows only the constant
- [ ] A live test against Ollama runs when its URL is configured
- [ ] One commit `S1-10: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Database state and the command (ticket 11); file and package levels (S5).
