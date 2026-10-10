package summarize

// Failure reasons for a summary that produced no text.
const (
	// ReasonTooLarge is the pre-send guard: the request was estimated above
	// the context limit and was never sent (D-015).
	ReasonTooLarge = "too_large"
	// ReasonTruncated is the post-send guard: Ollama's prompt token count
	// reached num_ctx - num_predict (D-015).
	ReasonTruncated = "truncated"
	// ReasonHTTPError is a transport failure or a non-200 Ollama response.
	ReasonHTTPError = "http_error"
	// ReasonEmptyOutput is a 200 response with no text.
	ReasonEmptyOutput = "empty_output"
)

// Result is the outcome of summarizing one function. On success Text holds the
// summary and Reason is empty; on failure Text is empty and Reason is one of
// the Reason* constants.
type Result struct {
	Text   string
	Reason string
}

// OK reports whether the result carries a summary rather than a failure reason.
func (r Result) OK() bool { return r.Reason == "" }

// bytesPerToken is D-015's token estimate denominator: tokens = ceil(bytes/4).
const bytesPerToken = 4

// tooLargeNumerator and tooLargeDenominator express the 0.8 pre-send fraction
// as exact integer arithmetic: a request is too large when
// tooLargeDenominator*estimate > tooLargeNumerator*numCtx.
const (
	tooLargeNumerator   = 4
	tooLargeDenominator = 5
)

// EstimateTokens estimates the token count of s as ceil(len(s)/4), the
// offline, deterministic estimate of D-015 (architecture: "Token estimate").
func EstimateTokens(s string) int {
	return (len(s) + bytesPerToken - 1) / bytesPerToken
}

// requestTooLarge reports whether the whole request (system plus user) is
// estimated above 0.8 x numCtx and must not be sent.
func requestTooLarge(system, user string, numCtx int) bool {
	est := EstimateTokens(system + user)
	return tooLargeDenominator*est > tooLargeNumerator*numCtx
}

// promptTruncated reports whether a response's prompt token count reached the
// context window minus the output reserve.
func promptTruncated(promptTokens, numCtx, numPredict int) bool {
	return promptTokens >= numCtx-numPredict
}
