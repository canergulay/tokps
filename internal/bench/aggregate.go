package bench

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

// Summary holds the measured (non-warmup) results of a multi-run benchmark.
type Summary struct {
	Model   string
	Host    string
	Warmup  int      // number of discarded warmup runs
	Results []Result // measured runs, in order
}

// Stat summarizes a metric across the measured runs as a median plus the
// observed range. Min/Max are direction-agnostic — honest for both latency
// (TTFT, higher=worse) and throughput (TPS, higher=better) — and don't oversell
// percentile resolution at the small default run count.
type Stat struct {
	Min float64
	P50 float64
	Max float64
}

// RunN performs warmup discarded runs followed by `runs` measured runs against
// the same endpoint and returns their results. A warmup absorbs cold-start and
// connection setup so the measured numbers reflect steady state. Any request
// error aborts the whole benchmark (fail fast on auth/URL problems).
func RunN(ctx context.Context, cfg Config, runs, warmup int) (Summary, error) {
	if runs < 1 {
		runs = 1
	}
	for i := range warmup {
		if _, err := Run(ctx, cfg); err != nil {
			return Summary{}, fmt.Errorf("warmup run %d: %w", i+1, err)
		}
	}
	sum := Summary{Model: cfg.Model, Host: hostOf(cfg.URL), Warmup: warmup}
	for i := range runs {
		r, err := Run(ctx, cfg)
		if err != nil {
			return Summary{}, fmt.Errorf("run %d: %w", i+1, err)
		}
		sum.Results = append(sum.Results, r)
	}
	return sum, nil
}

// TTFT returns p50/p90 of time-to-first-token, in seconds.
func (s Summary) TTFT() Stat {
	return s.stat(func(r Result) float64 { return r.TTFT.Seconds() })
}

// GenTPS returns p50/p90 of the generation rate (tokens/sec).
func (s Summary) GenTPS() Stat {
	return s.stat(func(r Result) float64 { return r.TPS() })
}

// E2ETPS returns p50/p90 of the end-to-end rate (tokens/sec, incl. TTFT).
func (s Summary) E2ETPS() Stat {
	return s.stat(func(r Result) float64 { return r.EndToEndTPS() })
}

// ITL pools the inter-token gaps from every measured run and returns their p50
// and p95 in milliseconds. ok is false when no streaming gaps were recorded
// (non-streaming responses, or single-token outputs).
func (s Summary) ITL() (p50ms, p95ms float64, ok bool) {
	var gaps []float64
	for _, r := range s.Results {
		for _, d := range r.ITL {
			gaps = append(gaps, float64(d)/float64(time.Millisecond))
		}
	}
	if len(gaps) == 0 {
		return 0, 0, false
	}
	return percentile(gaps, 0.50), percentile(gaps, 0.95), true
}

// MedianOutputTokens returns the median output-token count across runs.
func (s Summary) MedianOutputTokens() int {
	return int(math.Round(s.stat(func(r Result) float64 { return float64(r.OutputTokens) }).P50))
}

// Streamed reports whether every measured run used the streaming path.
func (s Summary) Streamed() bool {
	for _, r := range s.Results {
		if !r.Streamed {
			return false
		}
	}
	return len(s.Results) > 0
}

// Exact reports whether every measured run had exact token counts from usage.
func (s Summary) Exact() bool {
	for _, r := range s.Results {
		if !r.TokensExact {
			return false
		}
	}
	return len(s.Results) > 0
}

// PromptTokens returns the prompt-token count (constant across runs), or -1.
func (s Summary) PromptTokens() int {
	if len(s.Results) == 0 {
		return -1
	}
	return s.Results[0].PromptTokens
}

func (s Summary) stat(sel func(Result) float64) Stat {
	vals := make([]float64, len(s.Results))
	for i, r := range s.Results {
		vals[i] = sel(r)
	}
	return Stat{
		Min: percentile(vals, 0),
		P50: percentile(vals, 0.50),
		Max: percentile(vals, 1),
	}
}

// percentile returns the p-th percentile (p in [0,1]) of vals using linear
// interpolation between closest ranks — the "type 7" method used by NumPy and
// Excel's PERCENTILE.INC. vals need not be sorted.
func percentile(vals []float64, p float64) float64 {
	n := len(vals)
	if n == 0 {
		return 0
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	if n == 1 || p <= 0 {
		return s[0]
	}
	if p >= 1 {
		return s[n-1]
	}
	rank := p * float64(n-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return s[lo]
	}
	return s[lo] + (rank-float64(lo))*(s[hi]-s[lo])
}
