package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetchRepoRequiresTag(t *testing.T) {
	err := fetchRepo(context.Background(), "unused", "", t.TempDir())
	if !errors.Is(err, errNoKubeTag) {
		t.Fatalf("err = %v, want errNoKubeTag", err)
	}
}

// TestFetchRepoIdempotent clones a local repo, so it needs git but no network.
func TestFetchRepoIdempotent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	src := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", src}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(src, "README"), []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "v1")
	run("tag", "v1.0.0")
	run("commit", "-q", "--allow-empty", "-m", "v2")
	run("tag", "v2.0.0")

	repoURL := "file://" + src
	dir := filepath.Join(t.TempDir(), "nested", "kubernetes")

	if err := fetchRepo(ctx, repoURL, "v1.0.0", dir); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if err := fetchRepo(ctx, repoURL, "v1.0.0", dir); err != nil {
		t.Fatalf("second fetch should skip: %v", err)
	}
	err := fetchRepo(ctx, repoURL, "v2.0.0", dir)
	if err == nil || !strings.Contains(err.Error(), "different revision") {
		t.Fatalf("fetch at other tag: err = %v, want different-revision error", err)
	}
}
