# P2-S9 - Packaging & release

## Goal
Make `lodestone` installable in one command on macOS arm64 and Linux amd64/arm64: native CI builds, GitHub Release artifacts with checksums, a Homebrew tap, an install script, `go install` where possible, first-run UX, `uninstall`, and the tool README.

## Entry criteria
P2-S1 to P2-S8 done. The repository is public (D-044) and `gh repo view --json visibility` says `PUBLIC`.

## Links
[v2-architecture.md#distribution](../v2-architecture.md#distribution), [#commands](../v2-architecture.md#commands), [#repo-layout-of-the-tool](../v2-architecture.md#repo-layout-of-the-tool), [#model-manager](../v2-architecture.md#model-manager); [eval-protocol.md#memory-measurement](../eval-protocol.md#memory-measurement); decisions D-035, D-038, D-041, D-044, D-048.

## Tasks

### P2-S9-T01 - Native build and test matrix
- Why: cgo (tree-sitter, possibly the runtime) forbids casual cross-compiling (D-048).
- Depends on: none
- Touches: `.github/workflows/ci.yml`
- Details: jobs on `macos-14` (arm64), `ubuntu-latest` (amd64) and `ubuntu-24.04-arm` (arm64) each run `go build ./cmd/...`, `go vet`, `go test -count=1 ./...` (without Postgres-guarded tests except on the amd64 job with the service container), and upload the `lodestone` binary as an artifact. Windows is not built (D-035).
- Acceptance: a pushed branch shows all three jobs green and three binary artifacts; each job's log prints `go env GOOS GOARCH`.
- Tests: the CI jobs.
- Out of scope: releases.
- Owner: agent

### P2-S9-T02 - Release workflow with checksums
- Why: users download verified binaries.
- Depends on: P2-S9-T01
- Touches: `.github/workflows/release.yml`, `scripts/package.sh`
- Details: on a `v*` tag, each native job builds and packages `lodestone_<version>_<os>_<arch>.tar.gz` (binary, LICENSE, short README), a final job gathers them, writes `checksums.txt` (sha256), and creates a GitHub Release. `lodestone --version` prints version, commit and build date injected with `-ldflags`. The first real tag is pushed by the author (human decision), not by the workflow.
- Acceptance: a dry run on a `v0.0.0-test` tag in a fork or a `workflow_dispatch` run produces the three archives and `checksums.txt` as artifacts; `shasum -a 256 -c checksums.txt` passes; `lodestone --version` output matches the tag.
- Tests: the workflow run; a unit test for version formatting.
- Out of scope: Homebrew.
- Owner: agent

### P2-S9-T03 - Install script
- Why: `curl | sh` style install must be safe and verifiable.
- Depends on: P2-S9-T02
- Touches: `scripts/install.sh`
- Details: detects OS/arch, resolves the latest release (or `LODESTONE_VERSION`), downloads archive and `checksums.txt`, verifies the checksum (refuses on mismatch), installs to `~/.local/bin` (or `LODESTONE_INSTALL_DIR`), prints a PATH hint if needed, never uses sudo, and prints what it will do before doing it (`--yes` skips the prompt). Unsupported platforms exit with a clear message.
- Acceptance: `shellcheck scripts/install.sh` passes; tests run the script in Linux amd64 and arm64 containers against a local `httptest`-style file server serving a fake release (good checksum installs; tampered archive aborts with exit 1 and installs nothing).
- Tests: container-based script tests in CI.
- Out of scope: Windows.
- Owner: agent

### P2-S9-T04 - Homebrew tap
- Why: the usual macOS install path.
- Depends on: P2-S9-T02
- Touches: `packaging/homebrew/lodestone.rb.tmpl`, `scripts/update-formula.sh`, `.github/workflows/release.yml`
- Details: a formula template (url and sha256 per platform from `checksums.txt`, `bin.install "lodestone"`, `test do` runs `lodestone --version`). `update-formula.sh <version>` renders it; the release workflow opens a PR (or pushes with a deploy key) to the tap repository `homebrew-lodestone`. Creating the tap repository is a human step (P2-S9-T09).
- Acceptance: running `scripts/update-formula.sh v0.0.0-test` against a sample `checksums.txt` produces a formula that passes `brew audit --strict --formula` locally; the template has no hard-coded versions.
- Tests: script test with a fixture `checksums.txt` and golden formula output.
- Out of scope: publishing to homebrew-core.
- Owner: agent

### P2-S9-T05 - `go install` path
- Why: Go users expect it.
- Depends on: none
- Touches: `cmd/lodestone/`, `go.mod`, docs in the tool README (T08)
- Details: confirm `go install <module>/cmd/lodestone@latest` builds a working binary on macOS arm64 and Linux with a C toolchain; if a runtime library must be fetched at first run (D-053), `lodestone doctor --download` handles it. Document the C toolchain prerequisite and what happens without one.
- Acceptance: in a clean container with Go and gcc, `go install ./cmd/lodestone` followed by `lodestone --version` and `lodestone doctor` (which reports the missing model and the fix) succeeds; without gcc the failure message is captured in the README troubleshooting section.
- Tests: container check in CI.
- Out of scope: pure-Go build.
- Owner: agent

### P2-S9-T06 - First-run UX
- Why: the first minute decides whether users keep the tool.
- Depends on: P2-S2-T07
- Touches: `cmd/lodestone/main.go`, `cmd/lodestone/index.go`
- Details: `lodestone` with no args prints a 10-line help with the three commands to start (`index`, `search`, `connect`). First `index` in a repo with no model cached prints the model name, download size and cache path and asks `Download now? [Y/n]` (`--yes` accepts; non-TTY without `--yes` exits 2 with the exact command to run). After indexing it prints a 4-line next-steps block. All messages go to stderr; stdout stays machine-readable.
- Acceptance: golden transcripts for: no args, first index with consent, first index declined, non-TTY without `--yes`; each exits with the expected code.
- Tests: golden transcripts.
- Out of scope: telemetry (none exists and none is added).
- Owner: agent

### P2-S9-T07 - `uninstall`
- Why: cleanly removes what the tool created.
- Depends on: P2-S9-T06
- Touches: `cmd/lodestone/uninstall.go`
- Details: lists what it would remove (model cache dir, download partials, optionally per-repo `.lodestone/` directories passed with `--repo <path>`, MCP entries via `disconnect --all`) with sizes, asks for confirmation (`--yes` skips, `--dry-run` only lists), removes them, and prints how to remove the binary (`brew uninstall lodestone` or deleting the file). Never deletes anything outside those paths.
- Acceptance: temp-dir tests with a fake cache: `--dry-run` deletes nothing; confirmation deletes the cache; a path outside the known roots is refused; `--repo` removes only `<repo>/.lodestone`.
- Tests: temp-dir tests.
- Out of scope: removing the binary itself.
- Owner: agent

### P2-S9-T08 - Tool README and root README Phase 2 section
- Why: users need instructions and honest limits; the portfolio needs the Phase 2 story.
- Depends on: P2-S9-T06, P2-S8-T13
- Touches: `docs/lodestone/README.md`, `README.md` (Phase 2 section)
- Details: tool README: install (brew, script, go install), quick start, commands, MCP setup with `connect`, languages and tiers, resource numbers copied from `eval/results/phase2/resources.json` with the measurement method named for each claim (physical footprint on macOS, cgroup `memory.peak` on Linux; the 8GB claim is an inference from these), privacy (everything local; the only network access is the first model download and update checks do not exist), limitations (Windows unsupported, heuristic call graphs for non-Go languages, brute-force search). Root README gets a Phase 2 section that links to the tool README and the Phase 2 report section, and states that with 15 DEV queries the CIs are wide, so the model and dimension choice rests largely on the memory tie-breaker and is not described as a quality win.
- Acceptance: `scripts/check-readme-numbers.sh` (extended) verifies every resource number in the tool README against `resources.json`; `grep -n "not a quality win\|memory tie-breaker" README.md` finds the caution; all commands in the README appear in `lodestone --help`.
- Tests: the check script.
- Out of scope: marketing copy.
- Owner: agent

### P2-S9-T09 - [human] Create the tap repo, confirm public repo, cut the first release
- Why: outward-facing actions need the author.
- Depends on: P2-S9-T02, P2-S9-T04
- Touches: GitHub settings (outside the repo), this file (Results)
- Details: confirm the main repository is public and will stay public (D-044); create the public `homebrew-lodestone` repository and add the deploy credential the workflow needs; review the release workflow; check the name `lodestone` for collisions (`gh search repos lodestone`, `go list -m github.com/<owner>/lodestone@latest`, `brew search lodestone`) and record the outcome in D-034; push the first `v0.1.0` tag yourself. Record the release URL.
- Acceptance: D-034 holds the collision-check outcome; `gh repo view <owner>/<repo> --json visibility` prints `PUBLIC`; the release page lists three archives and `checksums.txt`; `brew install <owner>/lodestone/lodestone` works on your Mac and `lodestone --version` prints `0.1.0`.
- Tests: none.
- Out of scope: announcing the project.
- Owner: human

## Measurements to record
- Archive sizes per platform, install time from the script on a clean container, binary size (Results).

## Exit criteria
Three platform builds green; a release exists with verified checksums; brew, script and `go install` installs work; uninstall works; tool README numbers verified; ROADMAP updated.

## Risks + mitigations
- Free arm64 runners need a public repo: recorded in D-044; the human task verifies it.
- cgo toolchain issues for `go install`: documented prerequisites and tested in a container.

## Demo check
On a clean machine: `curl -fsSL <install-script-url> | sh`, `lodestone index` in a repo, `lodestone search "..."`, `lodestone connect`.

## Results
(empty)
