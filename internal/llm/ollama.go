package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultOllamaTimeout bounds a single completion. Local 7B models on Apple
// Silicon can take tens of seconds for a long prompt.
const DefaultOllamaTimeout = 2 * time.Minute

// Ollama is an LLM backed by a local Ollama server's /api/chat endpoint.
type Ollama struct {
	baseURL string
	model   string
	timeout time.Duration
	client  *http.Client
}

var _ LLM = (*Ollama)(nil)

// NewOllama returns a client for model at baseURL. A zero timeout uses
// DefaultOllamaTimeout.
func NewOllama(baseURL, model string, timeout time.Duration) *Ollama {
	if timeout <= 0 {
		timeout = DefaultOllamaTimeout
	}
	return &Ollama{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		timeout: timeout,
		client:  &http.Client{},
	}
}

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  ollamaOptions   `json:"options"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaOptions struct {
	Temperature float64 `json:"temperature"`
	NumPredict  int     `json:"num_predict,omitempty"`
}

type ollamaChatResponse struct {
	Model           string        `json:"model"`
	Message         ollamaMessage `json:"message"`
	PromptEvalCount int           `json:"prompt_eval_count"`
	EvalCount       int           `json:"eval_count"`
}

// Complete sends req to /api/chat without streaming.
func (o *Ollama) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()

	body := ollamaChatRequest{
		Model:   o.model,
		Stream:  false,
		Options: ollamaOptions{Temperature: req.Temperature, NumPredict: req.MaxTokens},
	}
	if req.System != "" {
		body.Messages = append(body.Messages, ollamaMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		body.Messages = append(body.Messages, ollamaMessage(m))
	}

	var resp ollamaChatResponse
	if err := postJSON(ctx, o.client, o.baseURL+"/api/chat", body, &resp); err != nil {
		return CompletionResponse{}, fmt.Errorf("ollama chat (%s): %w", o.model, err)
	}
	return CompletionResponse{
		Text:             resp.Message.Content,
		Model:            resp.Model,
		PromptTokens:     resp.PromptEvalCount,
		CompletionTokens: resp.EvalCount,
	}, nil
}

// ListModels returns the names of locally pulled models from /api/tags.
func (o *Ollama) ListModels(ctx context.Context) ([]string, error) {
	return ListOllamaModels(ctx, o.client, o.baseURL)
}

// ListOllamaModels returns the names of models pulled into the Ollama server
// at baseURL, e.g. "qwen2.5-coder:7b".
func ListOllamaModels(ctx context.Context, client *http.Client, baseURL string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/tags", http.NoBody)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, statusError(res)
	}
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(res.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("decode /api/tags: %w", err)
	}
	names := make([]string, 0, len(tags.Models))
	for _, m := range tags.Models {
		names = append(names, m.Name)
	}
	return names, nil
}

// HasModel reports whether want is among names. A name without a tag
// matches its ":latest" variant, as Ollama itself resolves it.
func HasModel(names []string, want string) bool {
	for _, n := range names {
		if n == want || (!strings.Contains(want, ":") && n == want+":latest") {
			return true
		}
	}
	return false
}

func postJSON(ctx context.Context, client *http.Client, url string, in, out any) error {
	payload, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return statusError(res)
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func statusError(res *http.Response) error {
	msg, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
	return fmt.Errorf("%s: %s", res.Status, strings.TrimSpace(string(msg)))
}
