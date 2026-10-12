// Package ci tests the Makefile's CI-facing targets (S1C-04). The tests run
// make against a stubbed tool and an unreachable model server; they never
// start, stop or contact a real Ollama.
package ci

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// runMake runs make in the repo root. Variables go on the command line because
// the Makefile's `-include .env` would override same-named environment values.
func runMake(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "make", append([]string{"-C", repoRoot(t)}, args...)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func fakeTool(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "fake-shellcheck")
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLintScriptsSaysSkippedWhenToolAbsent(t *testing.T) {
	out, err := runMake(t, "lint-scripts", "SHELLCHECK=no-such-shellcheck-tool")
	if err != nil {
		t.Fatalf("lint-scripts must not fail when the tool is absent: %v\n%s", err, out)
	}
	if !strings.Contains(out, "lint-scripts: skipped: not installed") {
		t.Fatalf("missing explicit skip line:\n%s", out)
	}
}

func TestLintScriptsRunsToolOnEveryScript(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args")
	tool := fakeTool(t, `echo "$@" > "`+log+`"`)
	out, err := runMake(t, "lint-scripts", "SHELLCHECK="+tool)
	if err != nil {
		t.Fatalf("lint-scripts failed: %v\n%s", err, out)
	}
	if strings.Contains(out, "skipped") {
		t.Fatalf("lint reported skipped although the tool is present:\n%s", out)
	}
	got, _ := os.ReadFile(log)
	if !strings.Contains(string(got), "scripts/summarize-pause.sh") {
		t.Fatalf("tool was not given scripts/summarize-pause.sh, args = %q", got)
	}
}

func TestLintScriptsFailsWhenToolReportsProblems(t *testing.T) {
	tool := fakeTool(t, `echo "SC9999 problem"; exit 1`)
	if out, err := runMake(t, "lint-scripts", "SHELLCHECK="+tool); err == nil {
		t.Fatalf("lint-scripts passed although the tool failed:\n%s", out)
	}
}

func TestCILocalRunsScriptLint(t *testing.T) {
	out, err := runMake(t, "-n", "ci-local")
	if err != nil {
		t.Fatalf("make -n ci-local: %v\n%s", err, out)
	}
	if !strings.Contains(out, "lint-scripts") {
		t.Fatalf("ci-local does not run lint-scripts:\n%s", out)
	}
}

func TestLiveTargetFailsLoudlyWhenServerUnreachable(t *testing.T) {
	// Port 1 on loopback is closed: nothing listens, connection is refused.
	out, err := runMake(t, "test-live", "OLLAMA_URL=http://127.0.0.1:1")
	if err == nil {
		t.Fatalf("test-live passed with an unreachable server:\n%s", out)
	}
	if !strings.Contains(out, "unreachable") || strings.Contains(out, "--- SKIP") {
		t.Fatalf("want a clear 'unreachable' message and no skip:\n%s", out)
	}
}

func TestLiveTestIsNotInTheDefaultRun(t *testing.T) {
	testFiles := func(tags string) string {
		cmd := exec.CommandContext(context.Background(), "go", "list", "-tags", tags, "-f", `{{join .TestGoFiles " "}}`, "./internal/summarize")
		cmd.Dir = repoRoot(t)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go list: %v\n%s", err, out)
		}
		return string(out)
	}
	if strings.Contains(testFiles(""), "live_test.go") {
		t.Fatal("the live test file is in the default build")
	}
	if !strings.Contains(testFiles("live"), "live_test.go") {
		t.Fatal("the live test file is missing under -tags live")
	}
	src, err := os.ReadFile(filepath.Join(repoRoot(t), "internal/summarize/live_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "t.Skip") {
		t.Fatal("the live test must fail, never skip")
	}
}
