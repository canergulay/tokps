package bench

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Summary holds the measured (non-warmup) results of a multi-run benchmark.
type Summary struct {
	Model       string
	Host        string
	Warmup      int       // number of discarded warmup runs (batches)
	Concurrency int       // streams fired in parallel per run (1 = sequential)
	Results     []Result  // every measured stream (runs × concurrency), in order
	BatchTPS    []float64 // aggregate tok/s per run (total output tokens ÷ batch wall)
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
// the same endpoint and returns their results. Each run is a batch of
// `concurrency` parallel streams (concurrency 1 = sequential, the default). A
// warmup absorbs cold-start and connection setup so the measured numbers
// reflect steady state. Any request error aborts the whole benchmark (fail fast
// on auth/URL problems).
func RunN(ctx context.Context, cfg Config, runs, warmup, concurrency int) (Summary, error) {
	if runs < 1 {
		runs = 1
	}
	if concurrency < 1 {
		concurrency = 1
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	for i := range warmup {
		if _, _, err := runBatch(ctx, cfg, concurrency, now); err != nil {
			return Summary{}, fmt.Errorf("warmup batch %d: %w", i+1, err)
		}
	}
	sum := Summary{Model: cfg.Model, Host: hostOf(cfg.URL), Warmup: warmup, Concurrency: concurrency}
	for i := range runs {
		results, aggTPS, err := runBatch(ctx, cfg, concurrency, now)
		if err != nil {
			return Summary{}, fmt.Errorf("batch %d: %w", i+1, err)
		}
		sum.Results = append(sum.Results, results...)
		sum.BatchTPS = append(sum.BatchTPS, aggTPS)
	}
	return sum, nil
}

// ParseLevels parses a comma-separated list of concurrency levels (e.g.
// "1,2,4,8") into a slice of positive ints, for the sweep mode.
func ParseLevels(s string) ([]int, error) {
	var levels []int
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("invalid concurrency level %q", p)
		}
		if n < 1 {
			return nil, fmt.Errorf("concurrency level must be >= 1, got %d", n)
		}
		levels = append(levels, n)
	}
	if len(levels) == 0 {
		return nil, fmt.Errorf("no concurrency levels given")
	}
	return levels, nil
}

// RunSweep benchmarks the same endpoint at each concurrency level in turn,
// returning one Summary per level (the throughput-vs-load curve).
func RunSweep(ctx context.Context, cfg Config, runs, warmup int, levels []int) ([]Summary, error) {
	sums := make([]Summary, 0, len(levels))
	for _, c := range levels {
		s, err := RunN(ctx, cfg, runs, warmup, c)
		if err != nil {
			return nil, fmt.Errorf("concurrency %d: %w", c, err)
		}
		sums = append(sums, s)
	}
	return sums, nil
}

// runBatch runs `concurrency` requests in parallel and returns their results
// plus the aggregate generation rate (total output tokens ÷ batch wall time).
// Any stream error aborts the batch.
func runBatch(ctx context.Context, cfg Config, concurrency int, now func() time.Time) ([]Result, float64, error) {
	if concurrency <= 1 {
		r, err := Run(ctx, cfg)
		if err != nil {
			return nil, 0, err
		}
		return []Result{r}, r.EndToEndTPS(), nil
	}

	type outcome struct {
		r   Result
		err error
	}
	ch := make(chan outcome, concurrency)
	t0 := now()
	for range concurrency {
		go func() {
			r, err := Run(ctx, cfg)
			ch <- outcome{r, err}
		}()
	}

	var results []Result
	var firstErr error
	total := 0
	for range concurrency {
		o := <-ch
		if o.err != nil {
			if firstErr == nil {
				firstErr = o.err
			}
			continue
		}
		results = append(results, o.r)
		total += o.r.OutputTokens
	}
	if firstErr != nil {
		return nil, 0, firstErr
	}

	agg := 0.0
	if wall := now().Sub(t0).Seconds(); wall > 0 {
		agg = float64(total) / wall
	}
	return results, agg, nil
}

// TTFT returns the min/p50/max of time-to-first-token, in seconds.
func (s Summary) TTFT() Stat {
	return s.stat(func(r Result) float64 { return r.TTFT.Seconds() })
}

// GenTPS returns the min/p50/max of the generation rate (tokens/sec).
func (s Summary) GenTPS() Stat {
	return s.stat(func(r Result) float64 { return r.TPS() })
}

// E2ETPS returns the min/p50/max of the end-to-end rate (tokens/sec, incl. TTFT).
func (s Summary) E2ETPS() Stat {
	return s.stat(func(r Result) float64 { return r.EndToEndTPS() })
}

// AggregateTPS returns the min/p50/max of the per-run aggregate throughput
// (total output tokens across all concurrent streams ÷ batch wall time). It is
// meaningful only under concurrency > 1.
func (s Summary) AggregateTPS() Stat {
	return statOf(s.BatchTPS)
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
	sort.Float64s(gaps)
	return percentileSorted(gaps, 0.50), percentileSorted(gaps, 0.95), true
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

// RunCount returns the number of measured runs (batches) — one per BatchTPS
// sample. It falls back to the per-stream count for summaries built without
// batch data (e.g. directly in tests).
func (s Summary) RunCount() int {
	if len(s.BatchTPS) > 0 {
		return len(s.BatchTPS)
	}
	return len(s.Results)
}

func (s Summary) stat(sel func(Result) float64) Stat {
	vals := make([]float64, len(s.Results))
	for i, r := range s.Results {
		vals[i] = sel(r)
	}
	return statOf(vals)
}

// statOf sorts a copy of vals once and returns its min, median, and max.
func statOf(vals []float64) Stat {
	if len(vals) == 0 {
		return Stat{}
	}
	sorted := slices.Clone(vals)
	sort.Float64s(sorted)
	return Stat{
		Min: sorted[0],
		P50: percentileSorted(sorted, 0.50),
		Max: sorted[len(sorted)-1],
	}
}

// percentile returns the p-th percentile of vals; it sorts a copy first.
func percentile(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := slices.Clone(vals)
	sort.Float64s(sorted)
	return percentileSorted(sorted, p)
}

// percentileSorted returns the p-th percentile (p in [0,1]) of an already
// sorted slice, using linear interpolation between closest ranks — the
// "type 7" method used by NumPy and Excel's PERCENTILE.INC.
func percentileSorted(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 || p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[n-1]
	}
	rank := p * float64(n-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (rank-float64(lo))*(sorted[hi]-sorted[lo])
}
