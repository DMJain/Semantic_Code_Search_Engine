package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/github"
)

var testTag = time.Date(2026, 8, 26, 10, 44, 20, 0, time.UTC)

func writeFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// rangeFromQuery extracts the merged: date range from a search query. Keys are
// URL-encoded sorted, so the value is not necessarily last.
func rangeFromQuery(q string) (start, end time.Time, ok bool) {
	for _, f := range strings.Fields(q) {
		v, found := strings.CutPrefix(f, "merged:")
		if !found {
			continue
		}
		parts := strings.SplitN(v, "..", 2)
		if len(parts) != 2 {
			return time.Time{}, time.Time{}, false
		}
		s, err1 := time.Parse("2006-01-02", parts[0])
		e, err2 := time.Parse("2006-01-02", parts[1])
		if err1 != nil || err2 != nil {
			return time.Time{}, time.Time{}, false
		}
		return s, e, true
	}
	return time.Time{}, time.Time{}, false
}

func TestMineYieldMissingToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	err := runMineYield(context.Background(), nil)
	if !errors.Is(err, errNoToken) {
		t.Fatalf("err = %v, want errNoToken", err)
	}
	if got, want := err.Error(), "set GITHUB_TOKEN (read-only, public repos) in .env"; got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

func TestMineYieldCountsEligiblePR(t *testing.T) {
	kubeDir := t.TempDir()
	writeFixture(t, kubeDir, "pkg/scheduler/sched.go", "package scheduler\n")

	var searchCalls, fileCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rate_limit":
			fmt.Fprint(w, `{"resources":{"search":{"limit":30,"remaining":30,"reset":0}}}`)
		case "/search/issues":
			atomic.AddInt32(&searchCalls, 1)
			_, end, ok := rangeFromQuery(r.URL.Query().Get("q"))
			if ok && !end.Before(time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)) {
				fmt.Fprint(w, `{"total_count":1,"items":[{"number":42,"title":"fix","pull_request":{"merged_at":"2026-08-25T00:00:00Z"}}]}`)
				return
			}
			fmt.Fprint(w, `{"total_count":0,"items":[]}`)
		case "/repos/kubernetes/kubernetes/pulls/42/files":
			atomic.AddInt32(&fileCalls, 1)
			fmt.Fprint(w, `[{"filename":"pkg/scheduler/sched.go","status":"modified"},{"filename":"pkg/scheduler/sched_test.go","status":"modified"}]`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := github.NewClient("tok", github.WithBaseURL(srv.URL))
	res, err := mine(context.Background(), c, mineOptions{
		repo: kubeRepo, tag: "v1.37.0", tagDate: testTag, months: 12, kubeDir: kubeDir,
	})
	if err != nil {
		t.Fatalf("mine: %v", err)
	}
	if res.Yield12m != 1 || res.Yield18mExtension != 0 {
		t.Errorf("yields = %d/%d, want 1/0", res.Yield12m, res.Yield18mExtension)
	}
	if res.ByPrefix["pkg/scheduler"] != 1 {
		t.Errorf("by_prefix = %v, want pkg/scheduler=1", res.ByPrefix)
	}
	if res.WindowsAtCap != 0 {
		t.Errorf("windows_at_cap = %d, want 0", res.WindowsAtCap)
	}
	if res.WindowEnd != "2026-08-26" || res.WindowStart != "2025-08-26" {
		t.Errorf("window = %s..%s, want 2025-08-26..2026-08-26", res.WindowStart, res.WindowEnd)
	}
	if fileCalls != 1 {
		t.Errorf("file calls = %d, want 1", fileCalls)
	}
}

func TestMineYieldRejectsOutOfSlicePR(t *testing.T) {
	kubeDir := t.TempDir()
	writeFixture(t, kubeDir, "pkg/api/types.go", "package api\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rate_limit":
			fmt.Fprint(w, `{"resources":{"search":{"limit":30,"remaining":30,"reset":0}}}`)
		case "/search/issues":
			_, end, _ := rangeFromQuery(r.URL.Query().Get("q"))
			if !end.Before(time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)) {
				fmt.Fprint(w, `{"total_count":1,"items":[{"number":9,"pull_request":{"merged_at":"2026-08-25T00:00:00Z"}}]}`)
				return
			}
			fmt.Fprint(w, `{"total_count":0,"items":[]}`)
		case "/repos/kubernetes/kubernetes/pulls/9/files":
			fmt.Fprint(w, `[{"filename":"pkg/api/types.go","status":"modified"}]`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := github.NewClient("tok", github.WithBaseURL(srv.URL))
	res, err := mine(context.Background(), c, mineOptions{
		repo: kubeRepo, tag: "v1.37.0", tagDate: testTag, months: 12, kubeDir: kubeDir,
	})
	if err != nil {
		t.Fatalf("mine: %v", err)
	}
	if res.Yield12m != 0 {
		t.Errorf("yield_12m = %d, want 0 (file outside slice)", res.Yield12m)
	}
}

func TestMineYieldSplitsCappedWindows(t *testing.T) {
	var spans []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rate_limit" {
			fmt.Fprint(w, `{"resources":{"search":{"limit":30,"remaining":30,"reset":0}}}`)
			return
		}
		start, end, ok := rangeFromQuery(r.URL.Query().Get("q"))
		if !ok {
			t.Errorf("no merged range in %q", r.URL.Query().Get("q"))
			return
		}
		days := int(end.Sub(start).Hours()/24) + 1
		spans = append(spans, days)
		if days >= 5 {
			fmt.Fprintf(w, `{"total_count":%d,"items":[]}`, github.MaxSearchResults)
			return
		}
		fmt.Fprint(w, `{"total_count":0,"items":[]}`)
	}))
	defer srv.Close()

	c := github.NewClient("tok", github.WithBaseURL(srv.URL))
	res, err := mine(context.Background(), c, mineOptions{
		repo: kubeRepo, tag: "v1.37.0", tagDate: testTag, months: 1, kubeDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("mine: %v", err)
	}
	if res.WindowsAtCap != 0 {
		t.Fatalf("windows_at_cap = %d, want 0 (all capped windows split)", res.WindowsAtCap)
	}
	var sawFull, sawSplit bool
	for _, d := range spans {
		if d == 7 {
			sawFull = true
		}
		if d < 7 {
			sawSplit = true
		}
	}
	if !sawFull || !sawSplit {
		t.Fatalf("spans = %v, want at least one 7-day and one split window", spans)
	}
}

func TestMineYieldSecondRunNoHTTP(t *testing.T) {
	dir := t.TempDir()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Path == "/rate_limit" {
			fmt.Fprint(w, `{"resources":{"search":{"limit":30,"remaining":30,"reset":0}}}`)
			return
		}
		fmt.Fprint(w, `{"total_count":0,"items":[]}`)
	}))
	defer srv.Close()

	opts := mineOptions{repo: kubeRepo, tag: "v1.37.0", tagDate: testTag, months: 1, kubeDir: t.TempDir()}
	first := github.NewClient("tok", github.WithBaseURL(srv.URL), github.WithCacheDir(dir))
	if _, err := mine(context.Background(), first, opts); err != nil {
		t.Fatalf("first mine: %v", err)
	}
	afterFirst := atomic.LoadInt32(&calls)
	if afterFirst == 0 {
		t.Fatal("first run made no HTTP calls; cache test is meaningless")
	}
	second := github.NewClient("tok", github.WithBaseURL(srv.URL), github.WithCacheDir(dir))
	if _, err := mine(context.Background(), second, opts); err != nil {
		t.Fatalf("second mine: %v", err)
	}
	if got := atomic.LoadInt32(&calls) - afterFirst; got != 0 {
		t.Errorf("second run made %d HTTP calls, want 0", got)
	}
}

func TestMineYieldTokenNotLogged(t *testing.T) {
	const token = "SECRET-TOKEN-VALUE-123"
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rate_limit" {
			fmt.Fprint(w, `{"resources":{"search":{"limit":30,"remaining":30,"reset":0}}}`)
			return
		}
		fmt.Fprint(w, `{"total_count":0,"items":[]}`)
	}))
	defer srv.Close()

	c := github.NewClient(token, github.WithBaseURL(srv.URL))
	if _, err := mine(context.Background(), c, mineOptions{
		repo: kubeRepo, tag: "v1.37.0", tagDate: testTag, months: 1, kubeDir: t.TempDir(),
	}); err != nil {
		t.Fatalf("mine: %v", err)
	}
	if strings.Contains(logged.String(), token) {
		t.Fatalf("token leaked into logs: %q", logged.String())
	}
	if !strings.Contains(logged.String(), "mine-yield start") {
		t.Errorf("expected a start log line, got %q", logged.String())
	}
}
