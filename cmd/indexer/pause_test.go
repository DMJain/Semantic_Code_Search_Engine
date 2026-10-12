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
	// Give bash time to exec so the command line is the stand-in's.
	time.Sleep(300 * time.Millisecond)
	return s
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
	cmd := exec.Command("sh", "-c", `sh "$0"; exit $?  # `+bin+` summarize --level function`, script)
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
