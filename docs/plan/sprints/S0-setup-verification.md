# S0 - Setup verification & fixes

## Goal
Make the existing skeleton trustworthy: verify what was built early, fix the audit findings, and prove the setup works end to end with a live Ollama.

## Entry criteria
Repo at commit `59a5039` (clean, `go build ./...` passes). Docker running. Ollama is needed from S0-T06 on.

## Links
- [architecture.md#data-model](../architecture.md#data-model), [#ollama-requests](../architecture.md#ollama-requests), [#stage-embed](../architecture.md#stage-embed)
- decisions: D-003, D-004, D-017, D-032

## Tasks

### S0-T01 - `make test` uses `-count=1`, baseline green
- Why: cached test results hide flaky integration tests.
- Depends on: none
- Touches: `Makefile`
- Details: change the `test` recipe to `go test -count=1 ./...` and update its comment. CI gets the same flag in S0-T04.
- Acceptance: running `make test` twice in a row prints no `(cached)`; `go vet ./...` and `make lint` report 0 issues.
- Tests: none new.
- Out of scope: new test targets, the race flag.
- Owner: agent

### S0-T02 - Verify migrations from a fresh volume
- Why: audit finding 3; migrations were only observed as no-ops on an existing database.
- Depends on: S0-T01
- Touches: `Makefile` (new `reset-db` target), `internal/store/migrate_integration_test.go`
- Details: add `reset-db: docker compose down -v && docker compose up -d --wait && go run ./cmd/indexer migrate`. Add `migrate-down` (`go run ./cmd/indexer migrate down`, one step; implement the down mode if the command lacks it). Run it. The integration test also runs goose Up, Down, Up once in a throwaway database.
- Acceptance: `make reset-db` exits 0; `make migrate-down migrate-up` exits 0 and `\dt` is unchanged; `psql "$DATABASE_URL" -c '\dt'` lists files, symbols, chunks, edges, summaries, embeddings, llm_cache, traces and `goose_db_version`; `\dx` shows `vector` and `pg_trgm`.
- Tests: the integration test covers Up/Down/Up when `TEST_DATABASE_URL` is set.
- Out of scope: new schema (S0-T03).
- Owner: agent

### S0-T03 - Migration 00002: `search_tsv` becomes a regular column
- Why: audit finding 1 (D-004); identifier splitting needs a Go-populated tsvector.
- Depends on: S0-T02
- Touches: `internal/store/migrations/00002_search_tsv_regular.sql`, `internal/store/migrate_integration_test.go`
- Details: Up: `ALTER TABLE chunks DROP COLUMN search_tsv;` (drops its GIN index), then `ALTER TABLE chunks ADD COLUMN search_tsv tsvector NOT NULL DEFAULT '';`, then `CREATE INDEX chunks_search_tsv_idx ON chunks USING gin (search_tsv);`. Down: drop the index and column, re-add the generated column exactly as in 00001, recreate the index. 00001 is NOT edited.
- Acceptance: after `make reset-db`, `\d chunks` shows `search_tsv | tsvector | not null default ''::tsvector` with no `generated` clause; `goose down` once then `up` once succeeds.
- Tests: integration test inserts a symbol and chunk with an explicit `search_tsv`, matches it with `@@ to_tsquery('simple','foo')`, and asserts `information_schema.columns.is_generated = 'NEVER'`.
- Out of scope: populating the column (S3).
- Owner: agent

### S0-T04 - CI Postgres service container
- Why: audit finding 2; the integration test never runs in CI.
- Depends on: S0-T03
- Touches: `.github/workflows/ci.yml`
- Details: in the `test` job add `services.postgres` (`pgvector/pgvector:pg17`, same user/db as compose, `pg_isready` health options) and `TEST_DATABASE_URL=postgres://codesearch:codesearch@localhost:5432/codesearch?sslmode=disable`. Use `go test -count=1 ./...`. Add a step that runs `go test -count=1 -v ./internal/store/...` and fails if the output contains `SKIP`. Remove the stale comment that says the test skips.
- Acceptance: a pushed branch shows the migration test as PASS (not SKIP) in the CI log and a green workflow.
- Tests: the CI step is the test.
- Out of scope: Ollama in CI.
- Owner: agent

### S0-T05 - Verify prefix templates against model cards; drop the third registry entry
- Why: a wrong prefix silently degrades retrieval; the templates were built unverified (D-003).
- Depends on: S0-T01
- Touches: `internal/embed/embed.go`, `internal/embed/ollama_test.go`, `.env.example`, `cmd/indexer/doctor.go`, `docs/plan/decisions.md` (D-003 outcome)
- Details: delete the registry entry of the third (provisional, dropped) embedding model and every mention of it (tests, `.env.example` comment, doctor text); only `qwen3-embedding:0.6b` and `nomic-embed-text` remain. For `qwen3-embedding:0.6b` and `nomic-embed-text` read the official model card (Hugging Face) and the Ollama library page. Record in D-003: card URL, exact query prefix, exact document prefix, native dim, MRL support yes/no, maximum context. Fix any mismatch in the registry and add one render test per model asserting the exact rendered strings.
- Acceptance: `go test -count=1 ./internal/embed/...` passes with one render test per registry model; a unit test asserts `len(embed.KnownModels()) == 2` and lists exactly `nomic-embed-text` and `qwen3-embedding:0.6b`; D-003 holds URL, both prefixes, dim, MRL, context and the embed `num_ctx` (rule in [#ollama-requests](../architecture.md#ollama-requests)) for both models; `! grep -rniI 'g[e]mma' . --exclude-dir=.git --exclude-dir=plan` exits 0 (no leftover mention in `.env.example`, tests or doctor text).
- Tests: table-driven render tests with golden strings copied from the cards.
- Out of scope: the live probe (S0-T07).
- Owner: agent

### S0-T06 - [human] Install Ollama and pull models
- Why: S0-T07 and S0-T08 and every later sprint need them.
- Depends on: none
- Touches: none (machine setup)
- Details: install Ollama, run `ollama serve`, then `ollama pull qwen2.5-coder:7b`, `ollama pull qwen3-embedding:0.6b`, `ollama pull nomic-embed-text`.
- Acceptance: `ollama list` shows all three; `curl -s localhost:11434/api/tags` returns them.
- Tests: none.
- Out of scope: tuning Ollama.
- Owner: human

### S0-T07 - Live probe for both embedding models
- Why: audit item "run the probe against a live Ollama for each model".
- Depends on: S0-T05, S0-T06
- Touches: `cmd/indexer/doctor.go`, `internal/embed/`
- Details: add `indexer doctor --probe-all`: for each model in `embed.KnownModels()` that is pulled, run the probe (no NaN/Inf, `len == Dim()`, L2 norm in [NormMin, NormMax]) on 3 fixed strings and print `model dim norm PASS|FAIL`. Models not pulled print `SKIP (not pulled)` with the pull command. A dimension different from the registry value fails and prints both numbers. Record the table in Results. Also add `indexer doctor --ollama-checks`, which (a) sends one identical 7B generation request twice and prints both `prompt_eval_count` values (prompt-cache effect, see D-015), and (b) sends an input longer than `num_ctx` to each embedding model with `truncate:false` and prints whether an error is returned.
- Acceptance: with Ollama running and both models pulled, `go run ./cmd/indexer doctor --probe-all` exits 0 and prints PASS twice; after `ollama rm nomic-embed-text` that model prints SKIP and the exit code is still 0, unless it is the configured `OLLAMA_EMBED_MODEL` (then non-zero); `doctor --ollama-checks` exits 0 and prints `gen_count_1`, `gen_count_2` and `embed_oversized: error` per model; the values are recorded in Results and, if the second generation count is lower, D-015 notes the cache effect.
- Tests: unit tests with `httptest.Server` (also for `--ollama-checks`: error on oversized embed input, two counts printed): wrong-dim vector (FAIL), NaN vector (FAIL), good vector (PASS). The live run is guarded by `OLLAMA_URL`.
- Out of scope: embedding quality comparison (S8).
- Owner: agent

### S0-T08 - Doctor live pass/fail acceptance
- Why: doctor was only tested with Ollama absent.
- Depends on: S0-T07
- Touches: `cmd/indexer/doctor.go` (only if fixes are needed)
- Details: run `make doctor` in two states and paste both transcripts into Results. State A: Ollama running, models pulled. State B: Ollama stopped.
- Acceptance: State A exits 0 with every check line OK (Postgres, extensions, Ollama reachable, summary model, embed model, probe). State B exits non-zero, names the failing check, prints the fix message (`start Ollama: ollama serve`) and still reports the Postgres checks.
- Tests: unit test for the Ollama-unreachable path (closed `httptest` server) asserting the fix message text.
- Out of scope: new checks.
- Owner: agent

### S0-T09 - Docs housekeeping
- Why: audit finding 5, D-017, and stale placeholders that create two sources of truth.
- Depends on: none
- Touches: `AGENTS.md` (one line), `docs/agents/domain.md`, `docs/architecture.md` (delete), `eval/queries.jsonl` (delete), `README.md` (links only)
- Details: (1) check that `AGENTS.md` and `CLAUDE.md` contain no `@path` imports and that `docs/agents/*` appears only as plain links; record the result in D-017. (1b) In `AGENTS.md` replace the "Domain docs" line with: `Domain docs: glossary `docs/plan/glossary.md`, decisions `docs/plan/decisions.md`. See `docs/agents/domain.md`.` (2) In `docs/agents/domain.md` replace `CONTEXT.md` with `docs/plan/glossary.md` and `docs/adr/` with `docs/plan/decisions.md`. (3) Delete `docs/architecture.md` and `eval/queries.jsonl`. (4) Fix any link in `README.md` that pointed at them. `docs/agents/issue-tracker.md` stays unchanged (D-032).
- Acceptance: `grep -rn "@" AGENTS.md CLAUDE.md | grep -v "@latest"` shows no import lines; `grep -rn "CONTEXT.md\|docs/adr" docs/agents/` returns nothing; `test ! -e docs/architecture.md && test ! -e eval/queries.jsonl`; `grep -rn "docs/architecture.md\|queries.jsonl" --include=*.md --include=*.go --include=Makefile --exclude-dir=plan --exclude-dir=.scratch --exclude-dir=.git .` returns nothing; `grep -n "CONTEXT.md\|docs/adr" AGENTS.md` returns nothing.
- Tests: none.
- Out of scope: the final README (S8).
- Owner: agent

### S0-T10 - S0 sign-off run
- Why: one recorded green run proves the exit criteria.
- Depends on: S0-T04, S0-T08, S0-T09
- Touches: this file (Results)
- Details: run `make reset-db`, `go vet ./...`, `make lint`, `TEST_DATABASE_URL=... make test`, `make doctor`, `go run ./cmd/indexer doctor --probe-all`. Paste summaries into Results and confirm CI is green on the pushed commit.
- Acceptance: every command exits 0; Results is filled; the CI run URL is recorded.
- Tests: none.
- Out of scope: new features.
- Owner: agent

## Measurements to record
- Probe table: model, dim, norm, PASS/FAIL (Results).
- D-003 verification record per model (decisions.md).
- Doctor transcripts, states A and B (Results).

## Exit criteria
All ten tasks done; CI runs the migration integration test; D-003 outcome filled; ROADMAP status updated.

## Risks + mitigations
- Model card prefix differs from what Ollama applies: the registry always renders prefixes itself and the test pins the exact strings.
- CI Postgres service slow to start: use `pg_isready` health options.

## Demo check
`make reset-db && make doctor && go run ./cmd/indexer doctor --probe-all` all green; stop Ollama and `make doctor` fails with the fix message.

## Results
Status 2026-10-10: signed off (local). All ten tasks T01-T10 are done and every local verification command is green. CI-green-on-a-pushed-branch proof is DEFERRED because the owner chose not to push (D-061); see the sign-off note.

### S0-T10 sign-off run (2026-10-10, local)
Executed in order; every command exited 0:
1. `make reset-db` exit 0. Emptiness guard printed `chunks=0 edges=0 embeddings=0 files=0 llm_cache=0 summaries=0 symbols=0 traces=0`, wiped the pg volume, restarted Postgres healthy, and applied both migrations: `applied migration version=1 file=00001_init.sql`, `applied migration version=2 file=00002_search_tsv_regular.sql`.
2. `go vet ./...` exit 0 (no findings).
3. `make lint` exit 0, `0 issues.`
4. `make test` exit 0, uncached and strict (`TEST_DATABASE_URL` loaded from `.env`): 45 `--- PASS`, 0 `--- FAIL`, 0 `--- SKIP`, 0 `(cached)`. Store package: `TestMigrateCreatesTables`, `TestSearchTSVIsPlainColumn`, `TestMigrateUpDownUpInThrowawayDatabase` all PASS.
5. `make doctor` exit 0, 8/8 PASS (State A transcript below).
6. `go run ./cmd/indexer doctor --probe-all` exit 0 (probe table below).
Post-reset schema (fresh volume): `goose_db_version` version_id 0,1,2 all applied; 9 tables (chunks, edges, embeddings, files, goose_db_version, llm_cache, summaries, symbols, traces); extensions `vector 0.8.7` and `pg_trgm 1.6`; `chunks.search_tsv` is `tsvector NOT NULL DEFAULT ''::tsvector`, `is_generated = NEVER`.
Doctor State A transcript of record: the 8/8 block below (T08). `--probe-all` probe table (T07): `nomic-embed-text dim 768 norm 1.0000 PASS`; `qwen3-embedding:0.6b dim 1024 norm 1.0000 PASS`.
References: D-003 outcome (verified prefixes/dims, decisions.md) confirmed present; D-060 (Ollama model IDs) confirmed present; D-061 (S0 process, push/draft-PR) confirmed present.
Sign-off note (deferral): the exit criterion "CI runs the migration integration test on a pushed branch / green CI on the final commit" is NOT satisfied locally-verifiable and is deferred. The owner explicitly chose not to push this branch, so no CI run URL and no draft-PR URL exist. Everything verifiable locally is green (commands 1-6 above, including the Postgres integration test running PASS not SKIP). The CI wiring itself (S0-T04) is in place in `.github/workflows/ci.yml` and the store integration test passes locally, so the deferred step is only the push/CI observation.

- S0-T06 (done): Ollama client 0.40.1 installed; `ollama list` shows all three: `qwen3-embedding:0.6b` (639 MB), `nomic-embed-text:latest` (274 MB) and `qwen2.5-coder:7b` (4.7 GB), so T06 acceptance (all three) is met. nomic-embed-text is now pulled (it was missing at spec time).
- S0-T07 probe table (`go run ./cmd/indexer doctor --probe-all`, exit 0):
  `nomic-embed-text dim 768 norm 1.0000 PASS`; `qwen3-embedding:0.6b dim 1024 norm 1.0000 PASS`.
- S0-T07 ollama-checks (`go run ./cmd/indexer doctor --ollama-checks`, exit 0): `gen_count_1 36`, `gen_count_2 36` (no prompt-cache reduction, see D-015 outcome); `embed_oversized: error (nomic-embed-text)`, `embed_oversized: error (qwen3-embedding:0.6b)`.
- S0-T07 model IDs/sizes (D-060, from `/api/tags`, Ollama 0.40.1): `qwen3-embedding:0.6b` digest `ac6da0dfba84a81fdbfbaf330198c33cd77c4cdfc53e8bc50eb581914a15621d` 639,150,858 B (dim 1024, configured `OLLAMA_EMBED_MODEL`); `nomic-embed-text:latest` digest `0a109f422b47e3a30ba2b10eca18548e944e8a23073ee3f3e947efcf3c45e59f` 274,302,450 B (dim 768); `qwen2.5-coder:7b` digest `dae161e27b0e90dd1856c8bb3209201fd6736d8eb66298e75ed87571486f4364` 4,683,087,561 B (summary model).
- State A doctor transcript (Postgres up via `make up`, Ollama up, two models pulled), exit 0:
  PASS config; PASS postgres (PostgreSQL 17.11); PASS vector 0.8.7; PASS pg_trgm 1.6; PASS ollama reachable; PASS model qwen2.5-coder:7b; PASS model qwen3-embedding:0.6b; PASS embedding probe (dim 1024, query norm 1.0000, doc norm 1.0000).
- Migrations (pre-sign-off): `goose_db_version` had versions 0 and 1 applied; tables: chunks, edges, embeddings, files, goose_db_version, llm_cache, summaries, symbols, traces. After the T10 `make reset-db`, version 2 (00002) is applied too, so all three versions are present (T03 done).
- `make lint`: 0 issues. `make test` is now uncached and strict (T01 done; see the T10 sign-off run above).
- S0-T08 install method detected: Ollama 0.40.1 is installed via Homebrew under `/opt/homebrew` but runs as a **manually started `ollama serve`** (pid seen in `pgrep -lf ollama`), NOT a `brew services` unit (`brew services list | grep ollama` shows `none`).
- S0-T08 State A doctor transcript (`make doctor`, Postgres up, Ollama up, all models pulled), exit 0, 8/8 PASS:
  ```
  PASS  config                       loaded
  PASS  postgres connection          PostgreSQL 17.11 (Debian 17.11-1.pgdg12+2)
  PASS  extension vector             version 0.8.7
  PASS  extension pg_trgm            version 1.6
  PASS  ollama reachable             http://localhost:11434
  PASS  model qwen2.5-coder:7b       pulled
  PASS  model qwen3-embedding:0.6b   pulled
  PASS  embedding probe              dim 1024, query norm 1.0000, doc norm 1.0000
  all checks passed
  ```
- S0-T08 State B doctor transcript (Ollama stopped, closed-port simulation), exit 1, Postgres checks still reported above the Ollama failure: `set -a; . ./.env; set +a; OLLAMA_URL=http://127.0.0.1:1 go run ./cmd/indexer doctor`:
  ```
  PASS  config                       loaded
  PASS  postgres connection          PostgreSQL 17.11 (Debian 17.11-1.pgdg12+2)
  PASS  extension vector             version 0.8.7
  PASS  extension pg_trgm            version 1.6
  FAIL  ollama reachable             Get "http://127.0.0.1:1/api/tags": dial tcp 127.0.0.1:1: connect: connection refused
        fix: install Ollama natively (brew install ollama, or https://ollama.com/download); start Ollama: ollama serve (or open the app); OLLAMA_URL is http://127.0.0.1:1
  SKIP  model qwen2.5-coder:7b       ollama unreachable
  SKIP  model qwen3-embedding:0.6b   ollama unreachable
  SKIP  embedding probe              ollama unreachable
  indexer doctor: 1 check(s) failed
  ```
  Closed-port-simulation note: the running `ollama serve` was NOT killed (owner-approved guard). The "Ollama stopped" state is represented by pointing `OLLAMA_URL` at a port with nothing listening (`http://127.0.0.1:1`). The fix message wording in `cmd/indexer/doctor.go` was adjusted minimally so it contains the exact substring `start Ollama: ollama serve` (install guidance and the `OLLAMA_URL` echo kept); the checklist text was unmet before this change.
- S0-T08 unit test: `TestDoctorOllamaUnreachableFixMessage` (cmd/indexer/doctor_test.go) starts an `httptest.Server`, captures its URL, closes it, then drives the doctor CLI against it; asserts non-zero exit, the failing check `FAIL  ollama reachable`, and the fix message substring `start Ollama: ollama serve`. `go test -count=1 -run TestDoctorOllamaUnreachableFixMessage -v ./cmd/indexer/` -> `--- PASS: TestDoctorOllamaUnreachableFixMessage`.
- S0-T08 end state: Ollama still running (`curl -s localhost:11434/api/tags` answers, `pgrep -lf ollama` shows `ollama serve`), `make doctor` 8/8 exit 0. `go vet ./...`, `make lint` (0 issues), `make test` all clean.
- T01-T10 all done as of the 2026-10-10 sign-off run above; `go vet`, `make lint`, `make test` all clean. Deferred (owner chose not to push): CI run URL and draft-PR URL (see sign-off note).

### S0 gap closure (2026-10-10, after the S0 code review; tickets 11-15 in `.scratch/S0/issues/`)
- S0-11 (templates pinned): `TestRenderedTemplateHashesArePinned` pins the six D-003 sha256 values through the public embedder. Mutation evidence, one-character edit of each template, each reverted afterwards (`git diff` empty):
  - qwen3 query, space added after `Query:` -> `--- FAIL: TestRequestBodies`, `--- FAIL: TestRenderedTemplateHashesArePinned`
  - qwen3 document, title separator `\n` -> space -> both fail
  - nomic query, `search_query: ` -> `search_query:` -> `TestRequestBodies`, `TestEmbedPrefixesAndNormalizes`, `TestRenderedTemplateHashesArePinned` fail
  - nomic document, `search_document: ` -> `search_documents: ` -> the same three fail
  Probe norm range now 0.99 to 1.01; `doctor --probe-all` still `nomic-embed-text dim 768 norm 1.0000 PASS`, `qwen3-embedding:0.6b dim 1024 norm 1.0000 PASS`, exit 0. Qwen3 format choice recorded as a D-003 outcome.
- S0-12 (embedding context size): the embed client had never sent a context size (D-015 requires it). The registry now holds `num_ctx` per model (qwen3 4096, nomic 2048), sent as `options.num_ctx` on every embed request, and `Ollama.NumCtx()` is exposed for the S3 guard. Live evidence (2026-10-10), filler text, `truncate:false`: nomic accepts 1,909 tokens and rejects about 2,860 for `num_ctx` unset, 2048, 4096 and 8192 alike (`ollama show nomic-embed-text` reports context length 2048); qwen3 accepts 3,812 and rejects about 4,200 with `num_ctx` unset, accepts 4,574 with 8192, rejects 2,288 with 1024. `go run ./cmd/indexer doctor --ollama-checks`, exit 0:
  ```
  gen_count_1 36
  gen_count_2 36
  embed_oversized: error (nomic-embed-text)
  embed_ctx_within: ok (nomic-embed-text, num_ctx 2048)
  embed_ctx_over: error (nomic-embed-text, num_ctx 2048)
  embed_oversized: error (qwen3-embedding:0.6b)
  embed_ctx_within: ok (qwen3-embedding:0.6b, num_ctx 4096)
  embed_ctx_over: error (qwen3-embedding:0.6b, num_ctx 4096)
  ```
  Tests: `TestNumCtxIsSentOnEveryEmbedRequest`, request-body tests with `options.num_ctx`, and `TestOllamaChecksFailsWhenRegistryContextSizeIsWrong` (fake Ollama that accepts more than the registry says makes the doctor exit non-zero). Consequence for S3 (nomic request ceiling 1,638 estimated tokens) recorded in D-003 and on the S3 entry checklist.
