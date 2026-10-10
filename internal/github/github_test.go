package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock records sleeps and advances virtual time, so rate-limit tests
// never actually sleep.
type fakeClock struct {
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Sleep(d time.Duration) {
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
}

func jsonItems(from, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"number":%d}`, from+i)
	}
	return b.String()
}

func TestSearchAllPaginates(t *testing.T) {
	var pages []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rate_limit" {
			fmt.Fprint(w, `{"resources":{"search":{"limit":30,"remaining":30,"reset":0}}}`)
			return
		}
		if r.URL.Query().Get("per_page") != strconv.Itoa(PerPage) {
			t.Errorf("per_page = %q, want %d", r.URL.Query().Get("per_page"), PerPage)
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		pages = append(pages, page)
		n := PerPage
		if page == 3 {
			n = 50
		}
		fmt.Fprintf(w, `{"total_count":250,"items":[%s]}`, jsonItems((page-1)*PerPage, n))
	}))
	defer srv.Close()

	c := NewClient("tok", WithBaseURL(srv.URL), WithClock(&fakeClock{now: time.Unix(1, 0)}))
	items, capped, err := c.SearchAll(context.Background(), "q")
	if err != nil {
		t.Fatalf("SearchAll: %v", err)
	}
	if capped {
		t.Fatal("capped = true, want false")
	}
	if len(items) != 250 {
		t.Errorf("len(items) = %d, want 250", len(items))
	}
	if fmt.Sprint(pages) != "[1 2 3]" {
		t.Errorf("pages = %v, want [1 2 3]", pages)
	}
}

func TestSearchAllReportsCap(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rate_limit" {
			fmt.Fprint(w, `{"resources":{"search":{"limit":30,"remaining":30,"reset":0}}}`)
			return
		}
		calls++
		fmt.Fprintf(w, `{"total_count":%d,"items":[]}`, MaxSearchResults)
	}))
	defer srv.Close()

	c := NewClient("tok", WithBaseURL(srv.URL), WithClock(&fakeClock{now: time.Unix(1, 0)}))
	items, capped, err := c.SearchAll(context.Background(), "q")
	if err != nil {
		t.Fatalf("SearchAll: %v", err)
	}
	if !capped {
		t.Fatal("capped = false, want true")
	}
	if items != nil {
		t.Errorf("items = %v, want nil", items)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (a capped query is not paginated)", calls)
	}
}

func TestRateLimitWaitsOnInjectedClock(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	var rlCalls, searchCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rate_limit" {
			if atomic.AddInt32(&rlCalls, 1) == 1 {
				fmt.Fprintf(w, `{"resources":{"search":{"limit":30,"remaining":0,"reset":%d}}}`, base.Add(60*time.Second).Unix())
				return
			}
			fmt.Fprint(w, `{"resources":{"search":{"limit":30,"remaining":5,"reset":0}}}`)
			return
		}
		atomic.AddInt32(&searchCalls, 1)
		fmt.Fprint(w, `{"total_count":0,"items":[]}`)
	}))
	defer srv.Close()

	fake := &fakeClock{now: base}
	c := NewClient("tok", WithBaseURL(srv.URL), WithClock(fake))
	start := time.Now()
	if _, err := c.Search(context.Background(), "q", 1); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("real elapsed %v: the injected clock should not sleep for real", elapsed)
	}
	if len(fake.sleeps) != 1 || fake.sleeps[0] != 60*time.Second {
		t.Fatalf("sleeps = %v, want [1m0s]", fake.sleeps)
	}
	if rlCalls != 2 {
		t.Errorf("rate-limit calls = %d, want 2 (read, wait, re-read)", rlCalls)
	}
	if searchCalls != 1 {
		t.Errorf("search calls = %d, want 1", searchCalls)
	}
}

func TestCacheSecondRunMakesNoCalls(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Path == "/rate_limit" {
			fmt.Fprint(w, `{"resources":{"search":{"limit":30,"remaining":30,"reset":0}}}`)
			return
		}
		fmt.Fprint(w, `{"total_count":1,"items":[{"number":7}]}`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	ctx := context.Background()

	first := NewClient("tok", WithBaseURL(srv.URL), WithCacheDir(dir), WithClock(&fakeClock{now: time.Unix(1, 0)}))
	if _, _, err := first.SearchAll(ctx, "q"); err != nil {
		t.Fatalf("first SearchAll: %v", err)
	}
	afterFirst := atomic.LoadInt32(&calls)
	if afterFirst == 0 {
		t.Fatal("first run made no calls; cache test is meaningless")
	}

	second := NewClient("tok", WithBaseURL(srv.URL), WithCacheDir(dir), WithClock(&fakeClock{now: time.Unix(1, 0)}))
	items, _, err := second.SearchAll(ctx, "q")
	if err != nil {
		t.Fatalf("second SearchAll: %v", err)
	}
	if atomic.LoadInt32(&calls) != afterFirst {
		t.Errorf("second run made %d more calls, want 0", atomic.LoadInt32(&calls)-afterFirst)
	}
	if len(items) != 1 || items[0].Number != 7 {
		t.Errorf("items = %+v, want number 7", items)
	}
}

func TestRequestSendsTokenInAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"total_count":0,"items":[]}`)
	}))
	defer srv.Close()

	c := NewClient("tok", WithBaseURL(srv.URL), WithClock(&fakeClock{now: time.Unix(1, 0)}))
	if _, err := c.Search(context.Background(), "q", 1); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer tok")
	}
}

func TestRateLimitedResponseWaitsAndRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limited", http.StatusForbidden)
			return
		}
		fmt.Fprint(w, `[{"filename":"pkg/scheduler/sched.go"}]`)
	}))
	defer srv.Close()

	fake := &fakeClock{now: time.Unix(2_000_000, 0)}
	c := NewClient("tok", WithBaseURL(srv.URL), WithClock(fake))
	files, err := c.PullFiles(context.Background(), "kubernetes/kubernetes", 1, 1)
	if err != nil {
		t.Fatalf("PullFiles: %v", err)
	}
	if len(files) != 1 {
		t.Errorf("files = %v, want 1", files)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	if len(fake.sleeps) != 1 || fake.sleeps[0] != time.Second {
		t.Errorf("sleeps = %v, want [1s]", fake.sleeps)
	}
}
