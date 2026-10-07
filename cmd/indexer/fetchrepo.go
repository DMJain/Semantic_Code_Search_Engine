package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
)

const kubeRepoURL = "https://github.com/kubernetes/kubernetes.git"

func runFetchRepo(ctx context.Context, args []string) error {
	if err := parseFlags("fetch-repo", args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	return fetchRepo(ctx, kubeRepoURL, cfg.KubeTag, cfg.KubeDir)
}

// errNoKubeTag is returned when KUBE_TAG is unset.
var errNoKubeTag = errors.New("KUBE_TAG is not set: set it in .env to a stable Kubernetes release tag " +
	"(e.g. KUBE_TAG=v1.37.1; see https://github.com/kubernetes/kubernetes/releases)")

// fetchRepo shallow-clones repoURL at tag into dir. It does nothing if dir
// is already a checkout of tag, and refuses to touch a non-empty dir that
// is anything else.
func fetchRepo(ctx context.Context, repoURL, tag, dir string) error {
	if tag == "" {
		return errNoKubeTag
	}

	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return err
		}
	case err != nil:
		return err
	case len(entries) > 0:
		tags, err := git(ctx, dir, "tag", "--points-at", "HEAD")
		if err != nil {
			return fmt.Errorf("%s exists but is not a git checkout (%w); move or remove it, then rerun", dir, err)
		}
		if slices.Contains(strings.Fields(tags), tag) {
			slog.Info("kubernetes already checked out at tag, skipping", "dir", dir, "tag", tag)
			return nil
		}
		return fmt.Errorf("%s is checked out at a different revision (tags at HEAD: %q), not %s; remove it to re-fetch: rm -rf %s",
			dir, strings.TrimSpace(tags), tag, dir)
	}

	slog.Info("cloning kubernetes (shallow)", "tag", tag, "dir", dir)
	cmd := exec.CommandContext(ctx, "git", "-c", "advice.detachedHead=false",
		"clone", "--depth", "1", "--branch", tag, repoURL, dir)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone %s at %s: %w", repoURL, tag, err)
	}
	slog.Info("cloned kubernetes", "tag", tag, "dir", dir)
	return nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
