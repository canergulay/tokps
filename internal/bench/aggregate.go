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
	Warmup      int           // number of discarded warmup runs (batches)
	Concurrency int           // streams fired in parallel per run (1 = sequential)
	CostIn      float64       // USD per 1M input tokens (0 = not configured)
	CostOut     float64       // USD per 1M output tokens (0 = not configured)
	Results     []Result      // every successful measured stream, in order
	BatchTPS    []float64     // aggregate tok/s per run (total output tokens ÷ batch wall)
	Streams     int           // measured streams attempted (runs × concurrency); 0 if warmup failed
	Errors      []StreamError // streams that failed (Batch 0 = warmup)
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
// reflect steady state.
//
// A warmup failure aborts (fail fast on auth/URL problems). In measured
// batches a failed stream is recorded in Summary.Errors and the benchmark
// continues; only when no stream at all succeeds does RunN return an
// *AllFailedError. In both error cases the returned Summary is still
// populated so callers can report what happened.
func RunN(ctx context.Context, cfg Config, runs, warmup, concurrency int) (Summary, error) {
	return runOne(ctx, cfg, runs, warmup, concurrency, false)
}

// runOne is RunN with a choice of warmup strictness (see runner.warm).
func runOne(ctx context.Context, cfg Config, runs, warmup, concurrency int, tolerant bool) (Summary, error) {
	r := newRunner(cfg, runs, warmup, concurrency)
	if err := r.warm(ctx, tolerant); err != nil {
		return r.sum, err
	}
	for range r.runs {
		if err := r.measure(ctx); err != nil {
			return Summary{}, err
		}
	}
	return r.result()
}

// runner drives one benchmark a batch at a time, so RunCompare can interleave
// the measured batches of several targets.
type runner struct {
	cfg      Config
	now      func() time.Time
	runs     int
	warmup   int
	conc     int
	sum      Summary
	firstErr error
}

func newRunner(cfg Config, runs, warmup, concurrency int) *runner {
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
	if cfg.Client == nil {
		// One pooled client shared by warmup and every measured stream, so
		// warmup can establish the connections the timed runs reuse.
		cfg.Client = poolingClient(concurrency)
	}
	return &runner{
		cfg: cfg, now: now, runs: runs, warmup: warmup, conc: concurrency,
		sum: Summary{
			Model:       cfg.Model,
			Host:        hostOf(cfg.URL),
			Warmup:      warmup,
			Concurrency: concurrency,
			CostIn:      cfg.CostIn,
			CostOut:     cfg.CostOut,
		},
	}
}

// warm runs the discarded warmup batches. A strict warmup fails on any stream
// error — the canary for auth and URL mistakes. A tolerant one (later sweep
// levels and compare targets, already past the canary) only fails when every
// stream of a batch failed; a partial failure such as one 429 among eight
// streams is reported through Warnf and the benchmark goes on.
func (r *runner) warm(ctx context.Context, tolerant bool) error {
	for i := range r.warmup {
		results, _, errs := runBatch(ctx, r.cfg, r.conc, r.now)
		if len(errs) > 0 {
			if ctx.Err() != nil {
				return &InterruptedError{Completed: 0}
			}
			if !tolerant || len(results) == 0 {
				for _, err := range errs {
					r.sum.Errors = append(r.sum.Errors, newStreamError(0, err))
				}
				return fmt.Errorf("warmup batch %d: %w", i+1, errs[0])
			}
			if r.cfg.Warnf != nil {
				r.cfg.Warnf("warmup batch %d: %d/%d streams failed (%v); continuing", i+1, len(errs), r.conc, errs[0])
			}
		}
		r.cfg.progress(ProgressEvent{Phase: "warmup", Index: i + 1, Total: r.warmup, Concurrency: r.conc})
	}
	r.sum.Streams = r.runs * r.conc
	return nil
}

// measure runs the next measured batch, recording failed streams. It only
// returns an error when the context was cancelled.
func (r *runner) measure(ctx context.Context) error {
	i := len(r.sum.BatchTPS)
	results, aggTPS, errs := runBatch(ctx, r.cfg, r.conc, r.now)
	if len(errs) > 0 && ctx.Err() != nil {
		return &InterruptedError{Completed: i}
	}
	for _, err := range errs {
		if r.firstErr == nil {
			r.firstErr = err
		}
		r.sum.Errors = append(r.sum.Errors, newStreamError(i+1, err))
	}
	r.sum.Results = append(r.sum.Results, results...)
	r.sum.BatchTPS = append(r.sum.BatchTPS, aggTPS)
	r.cfg.progress(ProgressEvent{Phase: "run", Index: i + 1, Total: r.runs, Concurrency: r.conc, BatchTPS: aggTPS, Failed: len(errs)})
	return nil
}

// result returns the summary, with an *AllFailedError when no measured
// stream succeeded.
func (r *runner) result() (Summary, error) {
	if len(r.sum.Results) == 0 {
		return r.sum, &AllFailedError{Err: r.firstErr}
	}
	return r.sum, nil
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
//
// The first level is the canary: any error there aborts, so auth and URL
// problems fail fast. Later levels tolerate partial warmup failures (one 429
// among eight streams), and a level that fails outright is kept as a failed
// Summary (AllFailed() == true) so the curve measured so far is not lost.
// Interruption always aborts.
func RunSweep(ctx context.Context, cfg Config, runs, warmup int, levels []int) ([]Summary, error) {
	sums := make([]Summary, 0, len(levels))
	for i, c := range levels {
		s, err := runOne(ctx, withLabel(cfg, fmt.Sprintf("c=%d", c)), runs, warmup, c, i > 0)
		if err != nil && (i == 0 || isInterrupted(err)) {
			return nil, fmt.Errorf("concurrency %d: %w", c, err)
		}
		sums = append(sums, s)
	}
	return sums, nil
}

// runBatch runs `concurrency` requests in parallel and returns the successful
// results, the aggregate generation rate (successful streams' output tokens ÷
// batch wall time, 0 when none succeeded), and one error per failed stream.
func runBatch(ctx context.Context, cfg Config, concurrency int, now func() time.Time) ([]Result, float64, []error) {
	if concurrency <= 1 {
		r, err := Run(ctx, cfg)
		if err != nil {
			return nil, 0, []error{err}
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
	var errs []error
	total := 0
	for range concurrency {
		o := <-ch
		if o.err != nil {
			errs = append(errs, o.err)
			continue
		}
		results = append(results, o.r)
		total += o.r.OutputTokens
	}

	agg := 0.0
	if wall := now().Sub(t0).Seconds(); wall > 0 && total > 0 {
		agg = float64(total) / wall
	}
	return results, agg, errs
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

// Cost returns the min/p50/max of the estimated per-request cost across
// measured runs, in USD. Values are meaningful only when CostIn/CostOut were
// configured.
func (s Summary) Cost() Stat {
	return s.stat(func(r Result) float64 { return r.Cost })
}

// CostConfigured reports whether per-request cost was computed (either price
// was set on the Config).
func (s Summary) CostConfigured() bool {
	return s.CostIn > 0 || s.CostOut > 0
}

// ITL pools the inter-event gaps (between content-bearing SSE chunks) from
// every measured run and returns their p50 and p95 in milliseconds. ok is
// false when no streaming gaps were recorded (non-streaming responses, or
// single-chunk outputs).
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

// Reasoning reports whether any measured run produced thinking tokens.
func (s Summary) Reasoning() bool {
	for _, r := range s.Results {
		if r.Reasoning {
			return true
		}
	}
	return false
}

// HiddenReasoning reports whether thinking was billed but never streamed.
func (s Summary) HiddenReasoning() bool {
	for _, r := range s.Results {
		if r.HiddenReasoning {
			return true
		}
	}
	return false
}

// ReasoningExact reports whether every run's thinking count came from usage.
func (s Summary) ReasoningExact() bool {
	for _, r := range s.Results {
		if r.Reasoning && !r.ReasoningExact {
			return false
		}
	}
	return len(s.Results) > 0
}

// MedianReasoningTokens returns the median thinking-token count across runs.
func (s Summary) MedianReasoningTokens() int {
	return int(math.Round(s.stat(func(r Result) float64 { return float64(r.ReasoningTokens) }).P50))
}

// TTFA returns the min/p50/max of time-to-first-answer-token in seconds,
// over the runs that reached the answer, and how many did.
func (s Summary) TTFA() (Stat, int) {
	var vals []float64
	for _, r := range s.Results {
		if r.TTFA > 0 {
			vals = append(vals, r.TTFA.Seconds())
		}
	}
	return statOf(vals), len(vals)
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
