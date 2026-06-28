// Package report formats benchmark results for the terminal.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/canergulay/tokps/internal/bench"
)

// Format writes a human-readable summary of r to w.
func Format(w io.Writer, r bench.Result) {
	fmt.Fprintf(w, "\ntokps — %s @ %s\n\n", r.Model, r.Host)

	if r.PromptTokens >= 0 {
		fmt.Fprintf(w, "  prompt tokens     %d\n", r.PromptTokens)
	} else {
		fmt.Fprintf(w, "  prompt tokens     n/a\n")
	}

	label := "exact"
	if !r.TokensExact {
		label = "estimated"
	}
	fmt.Fprintf(w, "  output tokens     %d   (%s)\n", r.OutputTokens, label)

	if r.Streamed {
		fmt.Fprintf(w, "  time to first     %s\n", dur(r.TTFT))
		fmt.Fprintf(w, "  generation        %s\n", dur(r.GenTime))
	} else {
		fmt.Fprintf(w, "  time to first     n/a\n")
		fmt.Fprintf(w, "  generation        n/a\n")
	}
	fmt.Fprintf(w, "  total wall        %s\n\n", dur(r.TotalWall))

	fmt.Fprintf(w, "  TPS               %.1f tok/s   (generation)\n", r.TPS())
	fmt.Fprintf(w, "  end-to-end        %.1f tok/s   (incl. TTFT)\n\n", r.EndToEndTPS())
}

// FormatSummary writes a summary of a multi-run benchmark to w, reporting p50
// and the observed min–max range across the measured runs. A single measured
// run falls back to the detailed single-shot block, where percentiles would be
// meaningless. When detail is set, an inter-token-latency (ITL) line is added.
func FormatSummary(w io.Writer, s bench.Summary, detail bool) {
	if len(s.Results) <= 1 {
		if len(s.Results) == 1 {
			Format(w, s.Results[0])
			if detail {
				writeITL(w, s)
			}
		}
		return
	}

	conc := s.Concurrency > 1
	if conc {
		fmt.Fprintf(w, "\ntokps — %s @ %s  (concurrency %d, %d runs, %d warmup)\n\n", s.Model, s.Host, s.Concurrency, s.RunCount(), s.Warmup)
	} else {
		fmt.Fprintf(w, "\ntokps — %s @ %s  (%d runs, %d warmup)\n\n", s.Model, s.Host, s.RunCount(), s.Warmup)
	}

	if pt := s.PromptTokens(); pt >= 0 {
		fmt.Fprintf(w, "  prompt tokens     %d\n", pt)
	} else {
		fmt.Fprintf(w, "  prompt tokens     n/a\n")
	}

	label := "exact"
	if !s.Exact() {
		label = "estimated"
	}
	medianNote := "median"
	if conc {
		medianNote = "median/stream"
	}
	fmt.Fprintf(w, "  output tokens     %d   (%s, %s)\n\n", s.MedianOutputTokens(), label, medianNote)

	if conc {
		a := s.AggregateTPS()
		fmt.Fprintf(w, "  aggregate   p50 %.1f   range %.1f–%.1f   (tok/s, all streams)\n", a.P50, a.Min, a.Max)
	}

	per := ""
	if conc {
		per = ", per stream"
	}
	ttft, gen, e2e := s.TTFT(), s.GenTPS(), s.E2ETPS()
	if s.Streamed() {
		fmt.Fprintf(w, "  TTFT     p50 %s   range %s–%s\n", secs(ttft.P50), secs(ttft.Min), secs(ttft.Max))
	}
	fmt.Fprintf(w, "  TPS      p50 %.1f   range %.1f–%.1f   (generation, N-1%s)\n", gen.P50, gen.Min, gen.Max, per)
	fmt.Fprintf(w, "  e2e      p50 %.1f   range %.1f–%.1f   (incl. TTFT%s)\n", e2e.P50, e2e.Min, e2e.Max, per)
	if detail {
		writeITL(w, s)
	}
	fmt.Fprintln(w)
}

// writeITL appends the inter-token-latency line when streaming gaps exist.
func writeITL(w io.Writer, s bench.Summary) {
	if p50, p95, ok := s.ITL(); ok {
		fmt.Fprintf(w, "  ITL      p50 %s   p95 %s   (inter-token)\n", ms(p50), ms(p95))
	}
}

// FormatJSON writes a machine-readable summary of the benchmark to w.
func FormatJSON(w io.Writer, s bench.Summary) error {
	type rng struct {
		Min float64 `json:"min"`
		P50 float64 `json:"p50"`
		Max float64 `json:"max"`
	}
	type itl struct {
		P50 float64 `json:"p50"`
		P95 float64 `json:"p95"`
	}
	type run struct {
		OutputTokens int     `json:"output_tokens"`
		Exact        bool    `json:"exact"`
		TTFTSeconds  float64 `json:"ttft_s"`
		GenSeconds   float64 `json:"gen_s"`
		WallSeconds  float64 `json:"wall_s"`
		TPS          float64 `json:"tps"`
		E2ETPS       float64 `json:"e2e_tps"`
	}

	ttft, gen, e2e := s.TTFT(), s.GenTPS(), s.E2ETPS()
	out := struct {
		Model              string `json:"model"`
		Host               string `json:"host"`
		Runs               int    `json:"runs"`
		Warmup             int    `json:"warmup"`
		Concurrency        int    `json:"concurrency"`
		PromptTokens       int    `json:"prompt_tokens"`
		OutputTokensMedian int    `json:"output_tokens_median"`
		TokensExact        bool   `json:"tokens_exact"`
		Streamed           bool   `json:"streamed"`
		AggregateTPS       *rng   `json:"aggregate_tps,omitempty"`
		TTFTSeconds        rng    `json:"ttft_s"`
		TPS                rng    `json:"tps"`
		E2ETPS             rng    `json:"e2e_tps"`
		ITLMillis          *itl   `json:"itl_ms,omitempty"`
		RunsDetail         []run  `json:"runs_detail"`
	}{
		Model: s.Model, Host: s.Host, Runs: s.RunCount(), Warmup: s.Warmup,
		Concurrency:  max(s.Concurrency, 1),
		PromptTokens: s.PromptTokens(), OutputTokensMedian: s.MedianOutputTokens(),
		TokensExact: s.Exact(), Streamed: s.Streamed(),
		TTFTSeconds: rng{ttft.Min, ttft.P50, ttft.Max},
		TPS:         rng{gen.Min, gen.P50, gen.Max},
		E2ETPS:      rng{e2e.Min, e2e.P50, e2e.Max},
	}
	if s.Concurrency > 1 {
		a := s.AggregateTPS()
		out.AggregateTPS = &rng{a.Min, a.P50, a.Max}
	}
	if p50, p95, ok := s.ITL(); ok {
		out.ITLMillis = &itl{P50: p50, P95: p95}
	}
	for _, r := range s.Results {
		out.RunsDetail = append(out.RunsDetail, run{
			OutputTokens: r.OutputTokens, Exact: r.TokensExact,
			TTFTSeconds: r.TTFT.Seconds(), GenSeconds: r.GenTime.Seconds(),
			WallSeconds: r.TotalWall.Seconds(), TPS: r.TPS(), E2ETPS: r.EndToEndTPS(),
		})
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// FormatSweep writes the throughput-vs-concurrency curve (one row per level).
func FormatSweep(w io.Writer, sums []bench.Summary) {
	if len(sums) == 0 {
		return
	}
	fmt.Fprintf(w, "\ntokps — %s @ %s  (sweep, %d runs, %d warmup)\n\n", sums[0].Model, sums[0].Host, sums[0].RunCount(), sums[0].Warmup)
	fmt.Fprintf(w, "  %-11s   %-15s   %-8s   %s\n", "concurrency", "aggregate tok/s", "TTFT p50", "TPS p50/stream")
	for _, s := range sums {
		fmt.Fprintf(w, "  %-11d   %-15.1f   %-8s   %.1f\n", s.Concurrency, s.AggregateTPS().P50, secs(s.TTFT().P50), s.GenTPS().P50)
	}
	fmt.Fprintln(w)
}

// FormatSweepJSON writes the sweep as a JSON array, one object per level.
func FormatSweepJSON(w io.Writer, sums []bench.Summary) error {
	type level struct {
		Concurrency    int     `json:"concurrency"`
		AggregateTPS   float64 `json:"aggregate_tps"`
		TTFTP50Seconds float64 `json:"ttft_p50_s"`
		TPSP50         float64 `json:"tps_p50"`
		E2EP50         float64 `json:"e2e_p50"`
	}
	arr := make([]level, 0, len(sums))
	for _, s := range sums {
		arr = append(arr, level{
			Concurrency:    s.Concurrency,
			AggregateTPS:   s.AggregateTPS().P50,
			TTFTP50Seconds: s.TTFT().P50,
			TPSP50:         s.GenTPS().P50,
			E2EP50:         s.E2ETPS().P50,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(arr)
}

func dur(d time.Duration) string {
	return fmt.Sprintf("%.2f s", d.Seconds())
}

func secs(s float64) string {
	return fmt.Sprintf("%.2fs", s)
}

func ms(v float64) string {
	return fmt.Sprintf("%.1fms", v)
}
