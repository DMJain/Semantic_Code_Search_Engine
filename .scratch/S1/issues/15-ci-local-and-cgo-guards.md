# 15: `make ci-local` and CGO-safe CI (new) (S1-T15)

Plan: S1-T15 (`docs/plan/sprints/S1-corpus-symbols.md`)

**What to build:** `make ci-local` mirrors `ci.yml` (vet, `REQUIRE_TEST_DB=1 go test -v -count=1 ./...` with the SKIP check, lint, lint-ci) against the compose Postgres. `make lint-ci` runs a pinned actionlint through `go run` (`ACTIONLINT_VERSION`, `WORKFLOW` variable). `CGO_ENABLED=1` is set on the build, vet and test steps of `ci.yml`, in `make ci-local` and in the Makefile build target only.

**Blocked by:** None (blocks 04 only)

**Status:** ready-for-agent

**Spec user stories:** 1, 2, 3, 4

- [ ] `make ci-local` exits 0 with 0 SKIP
- [ ] `make lint-ci` exits 0; with `WORKFLOW=<scratch copy with a typo>` it exits non-zero
- [ ] The pinned version, why it was chosen (newest that builds under `GOTOOLCHAIN=local`), and the shellcheck-skipped note recorded in Results; `ci.yml` and `ci-local` command diff recorded
- [ ] One commit `S1-T15: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Pushing; Ollama in CI.
