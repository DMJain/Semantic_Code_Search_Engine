# 01: `make ci-local` and CGO-safe CI

Plan: S1-T15 (`docs/plan/sprints/S1-corpus-symbols.md`). Spec user stories: 1, 2, 3, 4, 5, 8.

**What to build:** The developer can run one command, `make ci-local`, that mirrors the CI workflow's steps and environment against the local Postgres and proves the project builds, vets, lints and tests with no skipped test. The workflow file is checked by a pinned linter that needs no installation. The C toolchain is enabled for build, vet and test only (never job-wide), so the parser added later compiles without touching the corpus build context (D-062). Checking a deliberately broken copy of the workflow must fail.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] `make ci-local` exits 0 and its output has no skipped test
- [ ] `make lint-ci` exits 0, and exits non-zero when pointed at a scratch copy of the workflow containing a typo
- [ ] The pinned linter version, why it was chosen (newest that builds with the local toolchain forced), and the note that its shell-script check is skipped when shellcheck is absent are recorded in the sprint Results
- [ ] The commands run by the workflow and by `ci-local` are diffed and any difference is explained in Results
- [ ] The C-toolchain setting appears only on the build, vet and test steps, in `ci-local` and in the Makefile build target
- [ ] One commit `S1-01: ...`; nothing pushed
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Pushing, running CI remotely, Ollama in CI.
