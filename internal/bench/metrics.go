package bench

import "time"

// Result holds the outcome of a single benchmark run.
type Result struct {
	Model        string
	Host         string
	PromptTokens int // -1 when unknown
	OutputTokens int
	TokensExact  bool // true when from usage, false when estimated from chunks
	TTFT         time.Duration
	GenTime      time.Duration // last content token - first content token
	TotalWall    time.Duration
	Streamed     bool            // false when the non-streaming fallback was used
	ITL          []time.Duration // inter-event gaps between content-bearing chunks (len = chunks-1)
	Cost         float64         // estimated per-request cost in USD (0 when not configured)

	// Reasoning is true when the model produced thinking tokens — streamed
	// in delta.reasoning_content or reported in usage.
	Reasoning bool
	// ReasoningTokens is the thinking share of OutputTokens: from
	// usage.completion_tokens_details when ReasoningExact, otherwise
	// apportioned by streamed text length.
	ReasoningTokens int
	ReasoningExact  bool
	// HiddenReasoning marks thinking the server billed but never streamed
	// (OpenAI o-series, gpt-5). Those tokens were generated before the first
	// visible token, so the generation rate excludes them.
	HiddenReasoning bool
	// TTFA is request send → first answer (delta.content) token; 0 when the
	// stream never reached the answer (e.g. thinking used the whole budget).
	TTFA time.Duration
}

// AnswerTokens is the non-thinking share of the output.
func (r Result) AnswerTokens() int { return r.OutputTokens - r.ReasoningTokens }

// streamedTokens counts the tokens generated inside the [first, last] visible
// token window — all of them, unless the thinking was hidden.
func (r Result) streamedTokens() int {
	if r.HiddenReasoning {
		return r.AnswerTokens()
	}
	return r.OutputTokens
}

// TPS is the headline generation rate over the decode phase. The first token
// is produced during TTFT, so the (tLast-tFirst) window spans N-1 token
// intervals; dividing by N-1 matches the standard serving-benchmark
// definition (vLLM, NVIDIA genai-perf, Anyscale llmperf). Hidden thinking
// tokens are excluded from N because they were generated before the window
// opened. It falls back to the end-to-end rate when the generation interval
// is unavailable.
func (r Result) TPS() float64 {
	if n := r.streamedTokens(); r.GenTime > 0 && n > 1 {
		return float64(n-1) / r.GenTime.Seconds()
	}
	return r.EndToEndTPS()
}

// EndToEndTPS is output tokens divided by total wall time.
func (r Result) EndToEndTPS() float64 {
	if r.TotalWall <= 0 {
		return 0
	}
	return float64(r.OutputTokens) / r.TotalWall.Seconds()
}
