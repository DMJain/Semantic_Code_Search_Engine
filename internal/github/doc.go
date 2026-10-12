// Package github is a small, reusable GitHub REST client for the eval mining
// probes (the mine-yield probe today, and later the eval-set builder). It
// speaks only the search, pull-files and rate-limit endpoints, uses the
// standard library, waits on an injected clock (tests never sleep) and caches
// every raw response on disk keyed by request URL, so a second run makes no
// HTTP calls.
//
// A rate-limited response (403/429) is waited out and retried once (D-071); the
// mine-yield probe depends on it to survive a long run.
//
// Test seams: the [Clock] interface and [WithClock] option replace the time
// source so tests never sleep, and [WithBaseURL] points the client at a fake
// server. They exist for tests; production code uses the defaults
// ([DefaultBaseURL] and the real clock).
package github
