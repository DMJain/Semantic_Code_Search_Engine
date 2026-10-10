package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/github"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/walk"
)

const (
	kubeRepo      = "kubernetes/kubernetes"
	defaultCache  = "eval/candidates/cache"
	defaultOutput = "eval/results/bench/mine-yield.json"
)

// errNoToken is returned (and printed verbatim by main) when GITHUB_TOKEN is
// unset, before any network call.
var errNoToken = errors.New("set GITHUB_TOKEN (read-only, public repos) in .env")

// mineResult is the JSON written to eval/results/bench/mine-yield.json.
type mineResult struct {
	Tag               string         `json:"tag"`
	WindowMonths      int            `json:"window_months"`
	WindowStart       string         `json:"window_start"`
	WindowEnd         string         `json:"window_end"`
	Yield12m          int            `json:"yield_12m"`
	Yield18mExtension int            `json:"yield_18m_extension"`
	ByPrefix          map[string]int `json:"by_prefix"`
	WindowsAtCap      int            `json:"windows_at_cap"`
	PRsScanned        int            `json:"prs_scanned"`
}

// window is a closed date range (UTC, inclusive on both ends) used for a
// merged: search.
type window struct {
	start time.Time
	end   time.Time
}

func (w window) days() int { return int(w.end.Sub(w.start).Hours()/24) + 1 }

func (w window) String() string {
	return w.start.Format("2006-01-02") + ".." + w.end.Format("2006-01-02")
}

// mineOptions are the inputs the probe needs (everything except the client).
type mineOptions struct {
	repo    string
	tag     string
	tagDate time.Time
	months  int
	kubeDir string
}

func runMineYield(ctx context.Context, args []string) error {
	fs := newFlagSet("mine-yield")
	months := fs.Int("window-months", 12, "how far back to mine, in months")
	cacheDir := fs.String("cache-dir", defaultCache, "raw response cache directory")
	out := fs.String("out", defaultOutput, "results JSON path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := config.LoadEnv()
	if cfg.GithubToken == "" {
		return errNoToken
	}

	tagDate, err := kubeTagDate(ctx, cfg.KubeDir, cfg.KubeTag)
	if err != nil {
		return err
	}

	client := github.NewClient(cfg.GithubToken, github.WithCacheDir(*cacheDir))
	res, err := mine(ctx, client, mineOptions{
		repo:    kubeRepo,
		tag:     cfg.KubeTag,
		tagDate: tagDate,
		months:  *months,
		kubeDir: cfg.KubeDir,
	})
	if err != nil {
		return err
	}
	if err := writeMineResult(*out, res); err != nil {
		return err
	}
	fmt.Printf("yield_12m=%d yield_18m_extension=%d windows_at_cap=%d\n",
		res.Yield12m, res.Yield18mExtension, res.WindowsAtCap)
	return nil
}

// kubeTagDate resolves the pinned tag's commit date from the local clone. It
// is a read-only git call; no network.
func kubeTagDate(ctx context.Context, kubeDir, tag string) (time.Time, error) {
	if tag == "" {
		return time.Time{}, errors.New("KUBE_TAG is not set: set it in .env to the pinned Kubernetes release tag")
	}
	out, err := exec.CommandContext(ctx, "git", "-C", kubeDir, "log", "-1", "--format=%cI", tag).Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("resolve date of tag %s in %s: %w", tag, kubeDir, err)
	}
	s := strings.TrimSpace(string(out))
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse tag date %q: %w", s, err)
	}
	return t.UTC(), nil
}

// mine walks the weekly windows (newest first), splitting any window at the
// search cap, and counts qualifying merged PRs.
func mine(ctx context.Context, client *github.Client, o mineOptions) (mineResult, error) {
	res := mineResult{Tag: o.tag, WindowMonths: o.months, ByPrefix: map[string]int{}}
	for _, p := range walk.SlicePrefixes {
		res.ByPrefix[p] = 0
	}

	queue := weeklyWindows(o.tagDate, o.months)
	res.WindowEnd = queue[0].end.Format("2006-01-02")
	res.WindowStart = queue[len(queue)-1].start.Format("2006-01-02")

	cutoff12 := dateOnly(o.tagDate).AddDate(0, -12, 0)
	seen := map[int]bool{}
	slog.Info("mine-yield start", "repo", o.repo, "tag", o.tag,
		"windows", len(queue), "from", res.WindowStart, "to", res.WindowEnd)

	for len(queue) > 0 {
		w := queue[0]
		queue = queue[1:]
		items, capped, err := client.SearchAll(ctx, searchQuery(o.repo, w))
		if err != nil {
			return res, err
		}
		if capped {
			if w.days() <= 1 {
				// A single day over the cap cannot be split; surface it.
				res.WindowsAtCap++
				continue
			}
			a, b := splitWindow(w)
			queue = append(queue, a, b)
			continue
		}
		for i := range items {
			it := items[i]
			if it.PullRequest == nil || seen[it.Number] {
				continue
			}
			seen[it.Number] = true
			res.PRsScanned++
			ok, prefixes, err := qualifies(ctx, client, o, it.Number)
			if err != nil {
				return res, err
			}
			if !ok {
				continue
			}
			if !mergedAt(it, w).Before(cutoff12) {
				res.Yield12m++
			} else {
				res.Yield18mExtension++
			}
			for _, p := range prefixes {
				res.ByPrefix[p]++
			}
		}
	}
	return res, nil
}

// qualifies reports whether a PR counts: 1-5 changed non-test non-generated
// Go files, all inside the candidate slice and all present at the pinned tag.
func qualifies(ctx context.Context, client *github.Client, o mineOptions, number int) (bool, []string, error) {
	files, err := client.AllPullFiles(ctx, o.repo, number)
	if err != nil {
		return false, nil, err
	}
	kept := 0
	var prefixes []string
	for _, f := range files {
		p := f.Filename
		if !strings.HasSuffix(p, ".go") {
			continue
		}
		if _, excluded := walk.Excluded(o.kubeDir, p); excluded {
			continue // test, generated, vendor or build-constraint file
		}
		if _, err := os.Stat(filepath.Join(o.kubeDir, filepath.FromSlash(p))); err != nil {
			return false, nil, nil // must exist at the pinned tag
		}
		if !walk.InSlice(p) {
			return false, nil, nil // every kept file must be in the slice
		}
		kept++
		for _, pre := range walk.SlicePrefixes {
			if p == pre || strings.HasPrefix(p, pre+"/") {
				prefixes = append(prefixes, pre)
			}
		}
	}
	if kept < 1 || kept > 5 {
		return false, nil, nil
	}
	return true, dedupe(prefixes), nil
}

// weeklyWindows returns closed 7-day windows covering [tag-<months>, tag],
// newest first.
func weeklyWindows(tagDate time.Time, months int) []window {
	end := dateOnly(tagDate)
	start := end.AddDate(0, -months, 0)
	var ws []window
	e := end
	for {
		s := e.AddDate(0, 0, -6)
		if s.Before(start) {
			s = start
		}
		ws = append(ws, window{start: s, end: e})
		if !s.After(start) {
			break
		}
		e = s.AddDate(0, 0, -1)
	}
	return ws
}

// splitWindow halves a window at its middle day; callers split until below
// the search cap or one day wide.
func splitWindow(w window) (window, window) {
	mid := w.start.AddDate(0, 0, (w.days()-1)/2)
	return window{w.start, mid}, window{mid.AddDate(0, 0, 1), w.end}
}

func searchQuery(repo string, w window) string {
	return fmt.Sprintf("repo:%s is:pr base:master linked:issue merged:%s..%s",
		repo, w.start.Format("2006-01-02"), w.end.Format("2006-01-02"))
}

func mergedAt(it github.SearchItem, w window) time.Time {
	if it.PullRequest != nil && it.PullRequest.MergedAt != nil {
		return it.PullRequest.MergedAt.UTC()
	}
	return w.end
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func writeMineResult(path string, res mineResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
