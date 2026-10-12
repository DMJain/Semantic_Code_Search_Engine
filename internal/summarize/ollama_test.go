package summarize

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSummarizeSendsNumCtx(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/chat" {
			t.Errorf("got %s %s, want POST /api/chat", r.Method, r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		fmt.Fprint(w, `{"message":{"role":"assistant","content":"Returns the sum of a and b."},"prompt_eval_count":12}`)
	}))
	defer srv.Close()

	got := NewClient(srv.URL, "m", 4096).Summarize(context.Background(), SystemPrompt, "summarize add")
	if !got.OK() {
		t.Fatalf("Summarize reason = %q, body = %s", got.Reason, body)
	}
	for _, want := range []string{`"num_ctx":4096`, `"num_predict":120`, `"temperature":0`, `"system"`, `"user"`} {
		if !strings.Contains(body, want) {
			t.Errorf("request body missing %s: %s", want, body)
		}
	}
}

func TestSummarizeTooLargeNotSent(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		fmt.Fprint(w, `{"message":{"content":"should not happen"}}`)
	}))
	defer srv.Close()

	const numCtx = 4096
	// 3318 estimated tokens = 0.81 x 4096 (3318*4 = 13272 bytes).
	user := strings.Repeat("x", 13272)
	got := NewClient(srv.URL, "m", numCtx).Summarize(context.Background(), "", user)
	if got.Reason != ReasonTooLarge {
		t.Fatalf("reason = %q, want %q", got.Reason, ReasonTooLarge)
	}
	if requests != 0 {
		t.Fatalf("server recorded %d requests, want 0", requests)
	}
}

func TestSummarizeWithinContextIsSent(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		fmt.Fprint(w, `{"message":{"content":"ok"},"prompt_eval_count":1}`)
	}))
	defer srv.Close()

	// 3276 estimated tokens = 0.799 x 4096, just under the limit.
	user := strings.Repeat("x", 3276*4)
	if got := NewClient(srv.URL, "m", 4096).Summarize(context.Background(), "", user); !got.OK() {
		t.Fatalf("reason = %q, want sent", got.Reason)
	}
	if requests != 1 {
		t.Fatalf("server recorded %d requests, want 1", requests)
	}
}

func TestSummarizeTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"message":{"content":"Something."},"prompt_eval_count":3976}`)
	}))
	defer srv.Close()

	got := NewClient(srv.URL, "m", 4096).Summarize(context.Background(), SystemPrompt, "small")
	if got.Reason != ReasonTruncated {
		t.Fatalf("reason = %q, want %q", got.Reason, ReasonTruncated)
	}
}

func TestSummarizeEmptyOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"message":{"role":"assistant","content":""},"prompt_eval_count":5}`)
	}))
	defer srv.Close()

	got := NewClient(srv.URL, "m", 4096).Summarize(context.Background(), SystemPrompt, "small")
	if got.Reason != ReasonEmptyOutput {
		t.Fatalf("reason = %q, want %q", got.Reason, ReasonEmptyOutput)
	}
}

func TestSummarizeHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"model not found"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	got := NewClient(srv.URL, "m", 4096).Summarize(context.Background(), SystemPrompt, "small")
	if got.Reason != ReasonHTTPError {
		t.Fatalf("reason = %q, want %q", got.Reason, ReasonHTTPError)
	}
}

func TestDefaultNumCtx(t *testing.T) {
	if DefaultNumCtx != 4096 {
		t.Errorf("DefaultNumCtx = %d, want 4096", DefaultNumCtx)
	}
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		fmt.Fprint(w, `{"message":{"content":"ok"},"prompt_eval_count":1}`)
	}))
	defer srv.Close()

	NewClient(srv.URL, "m", 0).Summarize(context.Background(), SystemPrompt, "small")
	if !strings.Contains(body, `"num_ctx":4096`) {
		t.Errorf("zero numCtx did not fall back to default: %s", body)
	}
}
