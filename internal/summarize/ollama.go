package summarize

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

// DefaultNumCtx is the summary request context size used when none is
// configured (OLLAMA_SUMMARY_NUM_CTX, D-015). Keep in sync with
// config.DefaultOllamaSummaryNumCtx.
const DefaultNumCtx = 4096

// DefaultOllamaTimeout bounds a single summary request, matching the other
// Ollama clients; without it a stalled connection would hang the overnight run.
const DefaultOllamaTimeout = 2 * time.Minute

const (
	// temperature is fixed at 0: summaries are deterministic.
	temperature = 0.0
	// numPredict caps the generated summary at 120 tokens.
	numPredict = 120
)

// Client summarizes functions through a local Ollama server's /api/chat
// endpoint. Every request carries an explicit context size (D-015).
type Client struct {
	baseURL string
	model   string
	numCtx  int
	timeout time.Duration
	client  *http.Client
}

// NewClient returns a summarization client for model at baseURL with the given
// context size. A numCtx of zero or less uses DefaultNumCtx.
func NewClient(baseURL, model string, numCtx int) *Client {
	if numCtx <= 0 {
		numCtx = DefaultNumCtx
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		numCtx:  numCtx,
		timeout: DefaultOllamaTimeout,
		client:  &http.Client{},
	}
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Options  chatOptions   `json:"options"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatOptions struct {
	Temperature float64 `json:"temperature"`
	NumPredict  int     `json:"num_predict"`
	NumCtx      int     `json:"num_ctx"`
}

type chatResponse struct {
	Message         chatMessage `json:"message"`
	PromptEvalCount int         `json:"prompt_eval_count"`
}

// Summarize sends the system and user messages and applies the D-015 guards: a
// request estimated above 0.8 x numCtx is not sent (ReasonTooLarge), and a
// response whose prompt token count reached numCtx - num_predict is
// ReasonTruncated. It never returns an error; transport and protocol failures
// map to ReasonHTTPError.
func (c *Client) Summarize(ctx context.Context, system, user string) Result {
	if requestTooLarge(system, user, c.numCtx) {
		return Result{Reason: ReasonTooLarge}
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req := chatRequest{
		Model:  c.model,
		Stream: false,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Options: chatOptions{Temperature: temperature, NumPredict: numPredict, NumCtx: c.numCtx},
	}
	var resp chatResponse
	if err := c.post(ctx, req, &resp); err != nil {
		return Result{Reason: ReasonHTTPError}
	}
	if promptTruncated(resp.PromptEvalCount, c.numCtx, numPredict) {
		return Result{Reason: ReasonTruncated}
	}
	if resp.Message.Content == "" {
		return Result{Reason: ReasonEmptyOutput}
	}
	return Result{Text: resp.Message.Content}
}

func (c *Client) post(ctx context.Context, in, out any) error {
	payload, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1024))
		return fmt.Errorf("ollama chat: %s", res.Status)
	}
	return json.NewDecoder(res.Body).Decode(out)
}
