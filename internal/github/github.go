// Package github is a small, reusable GitHub REST client for the eval mining
// probes (S1-T12 and, later, S4-T05). It speaks only the search, pull-files
// and rate-limit endpoints, uses the standard library, waits on an injected
// clock (tests never sleep) and caches every raw response on disk keyed by
// request URL, so a second run makes no HTTP calls.
package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the public GitHub REST API root.
const DefaultBaseURL = "https://api.github.com"

// PerPage is GitHub's maximum page size for search and pull-file endpoints.
const PerPage = 100

// MaxSearchResults is the hard cap the search API returns per query; a query
// with at least this many matches must be narrowed (split) by the caller.
const MaxSearchResults = 1000

// Clock is the time source the client sleeps on when it exhausts the search
// rate limit. An injected fake clock lets tests assert the wait without
// actually sleeping.
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

type realClock struct{}

func (realClock) Now() time.Time        { return time.Now() }
func (realClock) Sleep(d time.Duration) { time.Sleep(d) }

// Option configures a Client.
type Option func(*Client)

// WithBaseURL points the client at a different API root (tests use an
// httptest.Server).
func WithBaseURL(baseURL string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(baseURL, "/") }
}

// WithClock replaces the real clock.
func WithClock(clk Clock) Option {
	return func(c *Client) { c.clock = clk }
}

// WithCacheDir enables the on-disk raw-response cache under dir.
func WithCacheDir(dir string) Option {
	return func(c *Client) { c.cacheDir = dir }
}

// Client is a GitHub REST client. The zero value is not usable; call NewClient.
type Client struct {
	baseURL  string
	token    string
	http     *http.Client
	clock    Clock
	cacheDir string

	searchRemaining int
	searchReset     int64
}

// NewClient returns a client for the GitHub API. token may be empty for
// public, unauthenticated reads, but the eval probes require one.
func NewClient(token string, opts ...Option) *Client {
	c := &Client{
		baseURL:         DefaultBaseURL,
		token:           token,
		http:            &http.Client{Timeout: 30 * time.Second},
		clock:           realClock{},
		searchRemaining: -1,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// SearchItem is one item from /search/issues. PullRequest is non-nil for
// pull requests (the search can also return plain issues).
type SearchItem struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	HTMLURL     string `json:"html_url"`
	Body        string `json:"body"`
	PullRequest *struct {
		MergedAt *time.Time `json:"merged_at"`
	} `json:"pull_request"`
}

// SearchResponse is the decoded /search/issues body.
type SearchResponse struct {
	TotalCount int          `json:"total_count"`
	Items      []SearchItem `json:"items"`
}

// PullFile is one changed file from /pulls/{n}/files.
type PullFile struct {
	Filename string `json:"filename"`
	Status   string `json:"status"`
}

// Search returns one page of results for a search query.
func (c *Client) Search(ctx context.Context, query string, page int) (SearchResponse, error) {
	rawURL := c.searchURL(query, page)
	body, err := c.get(ctx, rawURL, true)
	if err != nil {
		return SearchResponse{}, err
	}
	var res SearchResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return SearchResponse{}, fmt.Errorf("decode search: %w", err)
	}
	return res, nil
}

// SearchAll returns every item for query, paging until the total is reached.
// capped reports that the query has at least MaxSearchResults matches, so the
// caller should split its window; SearchAll does not fetch the remaining
// pages in that case. The returned items are cached per page, so a rerun is
// free.
func (c *Client) SearchAll(ctx context.Context, query string) (items []SearchItem, capped bool, err error) {
	first, err := c.Search(ctx, query, 1)
	if err != nil {
		return nil, false, err
	}
	if first.TotalCount >= MaxSearchResults {
		return nil, true, nil
	}
	items = first.Items
	for page := 2; len(items) < first.TotalCount; page++ {
		res, err := c.Search(ctx, query, page)
		if err != nil {
			return nil, false, err
		}
		if len(res.Items) == 0 {
			break
		}
		items = append(items, res.Items...)
	}
	return items, false, nil
}

// PullFiles returns one page of a pull request's changed files.
func (c *Client) PullFiles(ctx context.Context, repo string, number, page int) ([]PullFile, error) {
	rawURL := c.pullFilesURL(repo, number, page)
	body, err := c.get(ctx, rawURL, false)
	if err != nil {
		return nil, err
	}
	var files []PullFile
	if err := json.Unmarshal(body, &files); err != nil {
		return nil, fmt.Errorf("decode pull %d files: %w", number, err)
	}
	return files, nil
}

// AllPullFiles returns every changed file of a pull request.
func (c *Client) AllPullFiles(ctx context.Context, repo string, number int) ([]PullFile, error) {
	var all []PullFile
	for page := 1; ; page++ {
		files, err := c.PullFiles(ctx, repo, number, page)
		if err != nil {
			return nil, err
		}
		all = append(all, files...)
		if len(files) < PerPage {
			return all, nil
		}
	}
}

// RateLimit is one resource bucket from /rate_limit.
type RateLimit struct {
	Limit     int   `json:"limit"`
	Remaining int   `json:"remaining"`
	Reset     int64 `json:"reset"`
}

// RateLimit reads the search bucket of GET /rate_limit. It is not cached: it
// reflects the live budget.
func (c *Client) RateLimit(ctx context.Context) (RateLimit, error) {
	body, err := c.request(ctx, c.baseURL+"/rate_limit")
	if err != nil {
		return RateLimit{}, err
	}
	var res struct {
		Resources struct {
			Search RateLimit `json:"search"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return RateLimit{}, fmt.Errorf("decode /rate_limit: %w", err)
	}
	return res.Resources.Search, nil
}

// get returns the cached body for rawURL when present, otherwise fetches it
// (respecting the search rate limit when search is true) and caches the raw
// body. Cache hits make no HTTP calls.
func (c *Client) get(ctx context.Context, rawURL string, search bool) ([]byte, error) {
	key := cacheKey(rawURL)
	if body, ok := c.cacheGet(key); ok {
		return body, nil
	}
	if search {
		if err := c.waitForSearch(ctx); err != nil {
			return nil, err
		}
	}
	body, err := c.request(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	c.cachePut(key, body)
	return body, nil
}

// request performs one authenticated GET and returns the body of a 200. A
// rate-limited response (403/429) is waited out once on the injected clock and
// retried, so a long run can survive the separate core bucket limit.
func (c *Client) request(ctx context.Context, rawURL string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		res, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if res.StatusCode == http.StatusOK {
			return body, nil
		}
		if attempt == 0 {
			if d, ok := c.retryDelay(res); ok {
				c.clock.Sleep(d)
				continue
			}
		}
		return nil, fmt.Errorf("github %s: %s: %s", rawURL, res.Status, strings.TrimSpace(string(body)))
	}
}

// retryDelay reports how long to wait before retrying a rate-limited response,
// preferring Retry-After over the reset epoch.
func (c *Client) retryDelay(res *http.Response) (time.Duration, bool) {
	if res.StatusCode != http.StatusForbidden && res.StatusCode != http.StatusTooManyRequests {
		return 0, false
	}
	if v := res.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second, true
		}
	}
	if res.Header.Get("X-RateLimit-Remaining") != "0" {
		return 0, false
	}
	reset, err := strconv.ParseInt(res.Header.Get("X-RateLimit-Reset"), 10, 64)
	if err != nil {
		return 0, false
	}
	if d := time.Unix(reset, 0).Sub(c.clock.Now()); d > 0 {
		return d, true
	}
	return 0, false
}

// waitForSearch blocks until the search bucket has budget, using the injected
// clock, then spends one unit. The limit is read from /rate_limit, never
// hard-coded.
func (c *Client) waitForSearch(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.searchRemaining < 0 {
		rl, err := c.RateLimit(ctx)
		if err != nil {
			return err
		}
		c.searchRemaining, c.searchReset = rl.Remaining, rl.Reset
	}
	if c.searchRemaining <= 0 {
		if d := time.Unix(c.searchReset, 0).Sub(c.clock.Now()); d > 0 {
			c.clock.Sleep(d)
		}
		rl, err := c.RateLimit(ctx)
		if err != nil {
			return err
		}
		c.searchRemaining, c.searchReset = rl.Remaining, rl.Reset
	}
	c.searchRemaining--
	return nil
}

func (c *Client) searchURL(query string, page int) string {
	v := url.Values{}
	v.Set("q", query)
	v.Set("per_page", strconv.Itoa(PerPage))
	v.Set("page", strconv.Itoa(page))
	return c.baseURL + "/search/issues?" + v.Encode()
}

func (c *Client) pullFilesURL(repo string, number, page int) string {
	v := url.Values{}
	v.Set("per_page", strconv.Itoa(PerPage))
	v.Set("page", strconv.Itoa(page))
	return fmt.Sprintf("%s/repos/%s/pulls/%d/files?%s", c.baseURL, repo, number, v.Encode())
}

// cacheKey is the sha256 of the request URL: the raw body is stored under
// <cacheDir>/<key>.json, one file per (query, window, page) or pull page.
func cacheKey(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:])
}

func (c *Client) cacheGet(key string) ([]byte, bool) {
	if c.cacheDir == "" {
		return nil, false
	}
	body, err := os.ReadFile(filepath.Join(c.cacheDir, key+".json"))
	if err != nil {
		return nil, false
	}
	return body, true
}

// cachePut writes the raw body atomically, so an interrupted run never leaves
// a half-written file that a rerun would read as valid.
func (c *Client) cachePut(key string, body []byte) {
	if c.cacheDir == "" {
		return
	}
	if err := os.MkdirAll(c.cacheDir, 0o755); err != nil {
		return
	}
	final := filepath.Join(c.cacheDir, key+".json")
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
	}
}
