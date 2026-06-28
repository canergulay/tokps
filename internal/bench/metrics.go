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
	Streamed     bool // false when the non-streaming fallback was used
}

// TPS is the headline generation rate (output tokens per second of
// generation time). It falls back to the end-to-end rate when generation
// time is unavailable.
func (r Result) TPS() float64 {
	if r.GenTime > 0 {
		return float64(r.OutputTokens) / r.GenTime.Seconds()
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
