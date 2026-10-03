// Package report formats benchmark results for the terminal.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
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

	fmt.Fprintf(w, "  output tokens     %d   (%s)\n", r.OutputTokens, exactLabel(r.TokensExact))
	if r.Reasoning {
		fmt.Fprintf(w, "  thinking          %d   (%s)\n", r.ReasoningTokens, thinkingNote(r.ReasoningExact, r.HiddenReasoning, false, r.AnswerTokens()))
	}

	if r.Streamed {
		fmt.Fprintf(w, "  time to first     %s\n", dur(r.TTFT))
		if r.Reasoning && !r.HiddenReasoning {
			if r.TTFA > 0 {
				fmt.Fprintf(w, "  first answer      %s   (after thinking)\n", dur(r.TTFA))
			} else {
				fmt.Fprintf(w, "  first answer      not reached   (%s)\n", notReachedHint)
			}
		}
		fmt.Fprintf(w, "  generation        %s\n", dur(r.GenTime))
	} else {
		fmt.Fprintf(w, "  time to first     n/a\n")
		fmt.Fprintf(w, "  generation        n/a\n")
	}
	fmt.Fprintf(w, "  total wall        %s\n", dur(r.TotalWall))
	if r.Cost > 0 {
		fmt.Fprintf(w, "  cost              %s  (per request)\n", usd(r.Cost))
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "  TPS               %.1f tok/s   (generation)\n", r.TPS())
	fmt.Fprintf(w, "  end-to-end        %.1f tok/s   (incl. TTFT)\n\n", r.EndToEndTPS())
}

// FormatSummary writes a summary of a multi-run benchmark to w, reporting p50
// and the observed min–max range across the measured runs. A single measured
// run falls back to the detailed single-shot block, where percentiles would be
// meaningless. When detail is set, an inter-token-latency (ITL) line is added.
func FormatSummary(w io.Writer, s bench.Summary, detail bool) {
	if len(s.Results) == 0 {
		return
	}
	if len(s.Results) == 1 && s.Failed() == 0 {
		Format(w, s.Results[0])
		if detail {
			writeITL(w, s)
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

	medianNote := "median"
	if conc {
		medianNote = "median/stream"
	}
	fmt.Fprintf(w, "  output tokens     %d   (%s, %s)\n", s.MedianOutputTokens(), exactLabel(s.Exact()), medianNote)
	if s.Reasoning() {
		fmt.Fprintf(w, "  thinking          %d   (%s)\n", s.MedianReasoningTokens(),
			thinkingNote(s.ReasoningExact(), s.HiddenReasoning(), true, s.MedianOutputTokens()-s.MedianReasoningTokens()))
	}
	if s.CostConfigured() {
		fmt.Fprintf(w, "  cost              %s  (median, per request)\n", usd(s.Cost().P50))
	}
	if s.Failed() > 0 {
		fmt.Fprintf(w, "  errors            %d/%d streams   (%s)\n", s.Failed(), s.StreamCount(), errorGroupsText(s))
	}
	fmt.Fprintln(w)

	if conc {
		a := s.AggregateTPS()
		streams := "all streams"
		if s.Failed() > 0 {
			streams = "successful streams"
		}
		fmt.Fprintf(w, "  aggregate   p50 %.1f   range %.1f–%.1f   (tok/s, %s)\n", a.P50, a.Min, a.Max, streams)
	}

	per := ""
	if conc {
		per = ", per stream"
	}
	ttft, gen, e2e := s.TTFT(), s.GenTPS(), s.E2ETPS()
	if s.Streamed() {
		fmt.Fprintf(w, "  TTFT     p50 %s   range %s–%s\n", secs(ttft.P50), secs(ttft.Min), secs(ttft.Max))
		writeAnswerLine(w, s)
	}
	fmt.Fprintf(w, "  TPS      p50 %.1f   range %.1f–%.1f   (generation, N-1%s)\n", gen.P50, gen.Min, gen.Max, per)
	fmt.Fprintf(w, "  e2e      p50 %.1f   range %.1f–%.1f   (incl. TTFT%s)\n", e2e.P50, e2e.Min, e2e.Max, per)
	if detail {
		writeITL(w, s)
	}
	fmt.Fprintln(w)
}

// notReachedHint explains a reasoning stream that never got to the answer.
const notReachedHint = "thinking used the whole budget — raise --max-tokens"

// thinkingNote annotates the thinking-token line: where the count came from
// and how much of the output was the answer.
func thinkingNote(exact, hidden, median bool, answer int) string {
	src := "estimated"
	if exact {
		src = "exact"
	}
	if median {
		src += ", median"
	}
	if hidden {
		return src + ", hidden — not streamed, excluded from TPS"
	}
	return fmt.Sprintf("%s; answer %d", src, answer)
}

// writeAnswerLine adds time-to-first-answer for reasoning models that
// stream their thinking: TTFT then only marks the first thinking token.
func writeAnswerLine(w io.Writer, s bench.Summary) {
	if !streamsThinking(s) {
		return
	}
	a, reached := s.TTFA()
	switch {
	case reached == 0:
		fmt.Fprintf(w, "  answer   not reached   (%s)\n", notReachedHint)
	case reached < len(s.Results):
		fmt.Fprintf(w, "  answer   p50 %s   range %s–%s   (first answer token; %d/%d runs reached it)\n",
			secs(a.P50), secs(a.Min), secs(a.Max), reached, len(s.Results))
	default:
		fmt.Fprintf(w, "  answer   p50 %s   range %s–%s   (first answer token, after thinking)\n", secs(a.P50), secs(a.Min), secs(a.Max))
	}
}

// writeITL appends the inter-token-latency line when streaming gaps exist.
func writeITL(w io.Writer, s bench.Summary) {
	if p50, p95, ok := s.ITL(); ok {
		fmt.Fprintf(w, "  ITL      p50 %s   p95 %s   (inter-chunk)\n", ms(p50), ms(p95))
	}
}

type jsonRange struct {
	Min float64 `json:"min"`
	P50 float64 `json:"p50"`
	Max float64 `json:"max"`
}

type jsonITL struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
}

type jsonRun struct {
	OutputTokens int     `json:"output_tokens"`
	Exact        bool    `json:"exact"`
	TTFTSeconds  float64 `json:"ttft_s"`
	TTFASeconds  float64 `json:"ttfa_s,omitempty"`
	Reasoning    int     `json:"reasoning_tokens,omitempty"`
	GenSeconds   float64 `json:"gen_s"`
	WallSeconds  float64 `json:"wall_s"`
	TPS          float64 `json:"tps"`
	E2ETPS       float64 `json:"e2e_tps"`
	CostUsd      float64 `json:"cost_usd"`
}

// jsonReasoning describes the thinking phase of a reasoning model.
type jsonReasoning struct {
	TokensMedian int        `json:"tokens_median"`
	Exact        bool       `json:"exact"`
	Hidden       bool       `json:"hidden"`
	TTFASeconds  *jsonRange `json:"ttfa_s,omitempty"`
	AnswerRuns   int        `json:"answer_runs"`
}

type jsonStreamError struct {
	Batch  int    `json:"batch"`
	Status int    `json:"status,omitempty"`
	Error  string `json:"error"`
}

// summaryJSON is the machine-readable shape of one Summary, shared by --json
// and the compare array so one consumer handles both.
type summaryJSON struct {
	Model              string            `json:"model"`
	Host               string            `json:"host"`
	Runs               int               `json:"runs"`
	Warmup             int               `json:"warmup"`
	Concurrency        int               `json:"concurrency"`
	Streams            int               `json:"streams"`
	Errors             int               `json:"errors"`
	ErrorRate          float64           `json:"error_rate"`
	PromptTokens       int               `json:"prompt_tokens"`
	OutputTokensMedian int               `json:"output_tokens_median"`
	TokensExact        bool              `json:"tokens_exact"`
	Streamed           bool              `json:"streamed"`
	Reasoning          *jsonReasoning    `json:"reasoning,omitempty"`
	AggregateTPS       *jsonRange        `json:"aggregate_tps,omitempty"`
	TTFTSeconds        jsonRange         `json:"ttft_s"`
	TPS                jsonRange         `json:"tps"`
	E2ETPS             jsonRange         `json:"e2e_tps"`
	CostUsd            *jsonRange        `json:"cost_usd,omitempty"`
	CostInPer1M        float64           `json:"cost_in_per_1m,omitempty"`
	CostOutPer1M       float64           `json:"cost_out_per_1m,omitempty"`
	ITLMillis          *jsonITL          `json:"itl_ms,omitempty"`
	RunsDetail         []jsonRun         `json:"runs_detail"`
	ErrorsDetail       []jsonStreamError `json:"errors_detail,omitempty"`
}

// toJSON builds the JSON view of s.
func toJSON(s bench.Summary) summaryJSON {
	ttft, gen, e2e := s.TTFT(), s.GenTPS(), s.E2ETPS()
	out := summaryJSON{
		Model: s.Model, Host: s.Host, Runs: s.RunCount(), Warmup: s.Warmup,
		Concurrency: max(s.Concurrency, 1),
		Streams:     s.Streams, Errors: s.Failed(), ErrorRate: s.ErrorRate(),
		PromptTokens: s.PromptTokens(), OutputTokensMedian: s.MedianOutputTokens(),
		TokensExact: s.Exact(), Streamed: s.Streamed(),
		TTFTSeconds: jsonRange{ttft.Min, ttft.P50, ttft.Max},
		TPS:         jsonRange{gen.Min, gen.P50, gen.Max},
		E2ETPS:      jsonRange{e2e.Min, e2e.P50, e2e.Max},
		RunsDetail:  []jsonRun{},
	}
	if s.Concurrency > 1 {
		a := s.AggregateTPS()
		out.AggregateTPS = &jsonRange{a.Min, a.P50, a.Max}
	}
	if s.CostConfigured() {
		c := s.Cost()
		out.CostUsd = &jsonRange{c.Min, c.P50, c.Max}
		out.CostInPer1M = s.CostIn
		out.CostOutPer1M = s.CostOut
	}
	if p50, p95, ok := s.ITL(); ok {
		out.ITLMillis = &jsonITL{P50: p50, P95: p95}
	}
	if s.Reasoning() {
		a, reached := s.TTFA()
		out.Reasoning = &jsonReasoning{
			TokensMedian: s.MedianReasoningTokens(), Exact: s.ReasoningExact(),
			Hidden: s.HiddenReasoning(), AnswerRuns: reached,
		}
		if reached > 0 {
			out.Reasoning.TTFASeconds = &jsonRange{a.Min, a.P50, a.Max}
		}
	}
	for _, r := range s.Results {
		run := jsonRun{
			OutputTokens: r.OutputTokens, Exact: r.TokensExact,
			TTFTSeconds: r.TTFT.Seconds(), GenSeconds: r.GenTime.Seconds(),
			WallSeconds: r.TotalWall.Seconds(), TPS: r.TPS(), E2ETPS: r.EndToEndTPS(),
			CostUsd: r.Cost,
		}
		if r.Reasoning {
			run.Reasoning = r.ReasoningTokens
			run.TTFASeconds = r.TTFA.Seconds()
		}
		out.RunsDetail = append(out.RunsDetail, run)
	}
	for _, e := range s.Errors {
		out.ErrorsDetail = append(out.ErrorsDetail, jsonStreamError{Batch: e.Batch, Status: e.Status, Error: e.Err})
	}
	return out
}

// FormatJSON writes a machine-readable summary of the benchmark to w.
func FormatJSON(w io.Writer, s bench.Summary) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(toJSON(s))
}

// FormatSweep writes the throughput-vs-concurrency curve (one row per level).
// A level with no successful stream renders as a failed row.
func FormatSweep(w io.Writer, sums []bench.Summary) {
	if len(sums) == 0 {
		return
	}
	fmt.Fprintf(w, "\ntokps — %s @ %s  (sweep, %d runs, %d warmup)\n\n", sums[0].Model, sums[0].Host, sums[0].RunCount(), sums[0].Warmup)
	fmt.Fprintf(w, "  %-11s   %-24s   %-22s   %-14s   %s\n", "concurrency", "aggregate tok/s (range)", "TTFT p50 (range)", "TPS p50/stream", "errors")
	for _, s := range sums {
		if s.AllFailed() {
			fmt.Fprintf(w, "  %-11d   failed (%s)\n", s.Concurrency, errorGroupsText(s))
			continue
		}
		fmt.Fprintf(w, "  %-11d   %-24s   %-22s   %-14.1f   %s\n",
			s.Concurrency, aggCell(s), ttftCell(s), s.GenTPS().P50, errorsCell(s))
	}
	fmt.Fprintln(w)
}

// FormatSweepJSON writes the sweep as a JSON array, one object per level.
func FormatSweepJSON(w io.Writer, sums []bench.Summary) error {
	type level struct {
		Concurrency    int     `json:"concurrency"`
		Failed         bool    `json:"failed"`
		Errors         int     `json:"errors"`
		ErrorRate      float64 `json:"error_rate"`
		AggregateTPS   float64 `json:"aggregate_tps"`
		TTFTP50Seconds float64 `json:"ttft_p50_s"`
		TPSP50         float64 `json:"tps_p50"`
		E2EP50         float64 `json:"e2e_p50"`
	}
	arr := make([]level, 0, len(sums))
	for _, s := range sums {
		arr = append(arr, level{
			Concurrency:    s.Concurrency,
			Failed:         s.AllFailed(),
			Errors:         s.Failed(),
			ErrorRate:      s.ErrorRate(),
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

// usd formats a dollar amount with precision that suits its magnitude,
// trimming insignificant trailing zeros off sub-cent values.
func usd(v float64) string {
	if v >= 1 {
		return fmt.Sprintf("$%.2f", v)
	}
	if v >= 0.01 {
		return fmt.Sprintf("$%.4f", v)
	}
	s := strings.TrimRight(fmt.Sprintf("%.6f", v), "0")
	return "$" + strings.TrimSuffix(s, ".")
}

func secs(s float64) string {
	return fmt.Sprintf("%.2fs", s)
}

func ms(v float64) string {
	return fmt.Sprintf("%.1fms", v)
}
