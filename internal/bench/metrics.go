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
	ITL          []time.Duration // inter-token (inter-chunk) gaps; streaming only, len = tokens-1
}

// TPS is the headline generation rate over the decode phase. The first token
// is produced during TTFT, so the (tLast-tFirst) window spans N-1 token
// intervals; dividing by OutputTokens-1 matches the standard serving-benchmark
// definition (vLLM, NVIDIA genai-perf, Anyscale llmperf). It falls back to the
// end-to-end rate when the generation interval is unavailable.
func (r Result) TPS() float64 {
	if r.GenTime > 0 && r.OutputTokens > 1 {
		return float64(r.OutputTokens-1) / r.GenTime.Seconds()
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
