package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOllamaComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/chat" {
			t.Errorf("got %s %s, want POST /api/chat", r.Method, r.URL.Path)
		}
		var req ollamaChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "m" || req.Stream {
			t.Errorf("model=%q stream=%v, want m/false", req.Model, req.Stream)
		}
		if len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[1].Content != "hi" {
			t.Errorf("messages = %+v", req.Messages)
		}
		if req.Options.Temperature != 0.2 || req.Options.NumPredict != 50 {
			t.Errorf("options = %+v", req.Options)
		}
		_, _ = w.Write([]byte(`{"model":"m","message":{"role":"assistant","content":"hello"},"prompt_eval_count":7,"eval_count":3,"done":true}`))
	}))
	defer srv.Close()

	got, err := NewOllama(srv.URL, "m", 0).Complete(context.Background(), CompletionRequest{
		System:      "be brief",
		Messages:    []Message{{Role: "user", Content: "hi"}},
		Temperature: 0.2,
		MaxTokens:   50,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	want := CompletionResponse{Text: "hello", Model: "m", PromptTokens: 7, CompletionTokens: 3}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestOllamaCompleteHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"model 'm' not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := NewOllama(srv.URL, "m", 0).Complete(context.Background(), CompletionRequest{})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want 404 body in error", err)
	}
}

func TestOllamaCompleteTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	_, err := NewOllama(srv.URL, "m", 50*time.Millisecond).Complete(context.Background(), CompletionRequest{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestListModelsAndHasModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"qwen2.5-coder:7b"},{"name":"nomic-embed-text:latest"}]}`))
	}))
	defer srv.Close()

	names, err := NewOllama(srv.URL+"/", "m", 0).ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	for want, ok := range map[string]bool{
		"qwen2.5-coder:7b":        true,
		"qwen2.5-coder":           false,
		"nomic-embed-text":        true,
		"nomic-embed-text:latest": true,
		"qwen3-embedding:0.6b":    false,
	} {
		if got := HasModel(names, want); got != ok {
			t.Errorf("HasModel(%q) = %v, want %v", want, got, ok)
		}
	}
}

func TestGroqNotImplemented(t *testing.T) {
	_, err := NewGroq("k", "m").Complete(context.Background(), CompletionRequest{})
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("err = %v, want ErrNotImplemented", err)
	}
}
