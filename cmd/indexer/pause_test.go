package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests run scripts/summarize-pause.sh against stand-in processes only.
// Each test uses a unique binary name in the lookup pattern (via
// SUMMARIZE_PAUSE_PATTERN), so a real "bin/indexer summarize" can never be
// matched, let alone signalled, by the tests.

type standIn struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func (s *standIn) alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

// startStandIn runs a long sleep whose command line is argv0 followed by args.
func startStandIn(t *testing.T, ignoreInt bool, argv0 string) *standIn {
	t.Helper()
	trap := ""
	if ignoreInt {
		trap = `trap "" INT; `
	}
	cmd := exec.Command("bash", "-c", trap+`exec -a "$0" sleep 60`, argv0)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &standIn{cmd: cmd, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(s.done) }()
	t.Cleanup(func() {
		if s.alive() {
			_ = cmd.Process.Kill()
			<-s.done
		}
	})
	waitForCommandLine(t, cmd.Process.Pid, argv0)
	return s
}

// waitForCommandLine polls until the process's command line starts with want,
// i.e. bash has exec'd the stand-in.
func waitForCommandLine(t *testing.T, pid int, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
		if strings.HasPrefix(strings.TrimSpace(string(out)), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pid %d never showed command line %q", pid, want)
}

func uniqueBin(t *testing.T) string {
	return fmt.Sprintf("bin/indexer-pausetest-%d-%d", os.Getpid(), time.Now().UnixNano()%100000)
}

// runPause runs the script the way the recipe does: inside a shell whose own
// command line contains the summarize text.
func runPause(t *testing.T, bin string, timeout string) (string, int) {
	t.Helper()
	script, err := filepath.Abs("../../scripts/summarize-pause.sh")
	if err != nil {
		t.Fatal(err)
	}
	return runCmd(t, bin, timeout, exec.Command("sh", "-c", `sh "$0"; exit $?  # `+bin+` summarize --level function`, script))
}

func runCmd(t *testing.T, bin, timeout string, cmd *exec.Cmd) (string, int) {
	t.Helper()
	cmd.Env = append(os.Environ(),
		"SUMMARIZE_PAUSE_PATTERN=^"+bin+" summarize( |$)",
		"SUMMARIZE_PAUSE_TIMEOUT="+timeout,
	)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running pause: %v", err)
	}
	return string(out), code
}

func TestPauseSignalsOnlyTheStandIn(t *testing.T) {
	bin := uniqueBin(t)
	real := startStandIn(t, false, bin+" summarize --level function --concurrency 1")
	wrapper := startStandIn(t, false, "caffeinate -i "+bin+" summarize --level function")
	decoy := startStandIn(t, false, "sh -c echo "+bin+" summarize")

	out, code := runPause(t, bin, "10")
	if code != 0 {
		t.Fatalf("exit %d, want 0; output:\n%s", code, out)
	}
	if !strings.Contains(out, "pid "+strconv.Itoa(real.cmd.Process.Pid)) {
		t.Fatalf("output does not name pid %d:\n%s", real.cmd.Process.Pid, out)
	}
	select {
	case <-real.done:
	case <-time.After(2 * time.Second):
		t.Fatal("stand-in was not signalled")
	}
	if !wrapper.alive() || !decoy.alive() {
		t.Fatalf("wrapper alive=%v decoy alive=%v; both must be untouched", wrapper.alive(), decoy.alive())
	}
}

func TestPauseNothingRunning(t *testing.T) {
	out, code := runPause(t, uniqueBin(t), "2")
	if code != 2 {
		t.Fatalf("exit %d, want 2; output:\n%s", code, out)
	}
	if !strings.Contains(out, "nothing running") {
		t.Fatalf("output lacks 'nothing running':\n%s", out)
	}
}

func TestPauseDidNotExitWithinTimeout(t *testing.T) {
	bin := uniqueBin(t)
	stuck := startStandIn(t, true, bin+" summarize --level function")

	out, code := runPause(t, bin, "1")
	if code != 3 {
		t.Fatalf("exit %d, want 3; output:\n%s", code, out)
	}
	if !strings.Contains(out, "did not exit") {
		t.Fatalf("output lacks 'did not exit':\n%s", out)
	}
	if !stuck.alive() {
		t.Fatal("stand-in ignoring SIGINT should still be running")
	}
}

// A grandparent of the script (pgrep itself already skips the direct parent) whose own command line matches the pattern must be
// skipped: only the script's ancestor guard protects it.
func TestPauseSkipsMatchingAncestor(t *testing.T) {
	bin := uniqueBin(t)
	script, err := filepath.Abs("../../scripts/summarize-pause.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c",
		`exec -a "$0" bash -c 'sh -c '"'"'sh "$0"; exit $?'"'"' "$1"; exit $?' _ "$1"`,
		bin+" summarize --level function", script)
	out, code := runCmd(t, bin, "2", cmd)
	if code != 2 || !strings.Contains(out, "nothing running") {
		t.Fatalf("exit %d, want 2 'nothing running'; output:\n%s", code, out)
	}
}
