package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/canergulay/tokps/internal/bench"
)

// FormatMarkdown writes the summary as a GitHub-flavored markdown table —
// the same numbers as FormatSummary, ready to paste into an issue or README.
func FormatMarkdown(w io.Writer, s bench.Summary, detail bool) {
	if len(s.Results) == 0 {
		return
	}
	if len(s.Results) == 1 && s.Failed() == 0 {
		formatMarkdownSingle(w, s, detail)
		return
	}
	title := fmt.Sprintf("**tokps — %s @ %s** (", s.Model, s.Host)
	if s.Concurrency > 1 {
		title += fmt.Sprintf("concurrency %d, ", s.Concurrency)
	}
	fmt.Fprintf(w, "%s%d runs, %d warmup)\n\n", title, s.RunCount(), s.Warmup)
	fmt.Fprintln(w, "| metric | p50 | min | max |")
	fmt.Fprintln(w, "|---|---|---|---|")
	if s.Concurrency > 1 {
		a := s.AggregateTPS()
		fmt.Fprintf(w, "| aggregate tok/s | %.1f | %.1f | %.1f |\n", a.P50, a.Min, a.Max)
	}
	if s.Streamed() {
		t := s.TTFT()
		fmt.Fprintf(w, "| TTFT | %s | %s | %s |\n", secs(t.P50), secs(t.Min), secs(t.Max))
		if streamsThinking(s) {
			if a, reached := s.TTFA(); reached > 0 {
				fmt.Fprintf(w, "| first answer | %s | %s | %s |\n", secs(a.P50), secs(a.Min), secs(a.Max))
			} else {
				fmt.Fprintln(w, "| first answer | not reached | | |")
			}
		}
	}
	g := s.GenTPS()
	fmt.Fprintf(w, "| TPS (gen) | %.1f | %.1f | %.1f |\n", g.P50, g.Min, g.Max)
	e := s.E2ETPS()
	fmt.Fprintf(w, "| e2e tok/s | %.1f | %.1f | %.1f |\n", e.P50, e.Min, e.Max)
	if detail {
		if p50, p95, ok := s.ITL(); ok {
			fmt.Fprintf(w, "| ITL | p50 %s, p95 %s | | |\n", ms(p50), ms(p95))
		}
	}
	fmt.Fprintf(w, "| output tokens | %d (%s) | | |\n", s.MedianOutputTokens(), exactLabel(s.Exact()))
	if s.Reasoning() {
		fmt.Fprintf(w, "| thinking tokens | %d (%s) | | |\n", s.MedianReasoningTokens(),
			thinkingNote(s.ReasoningExact(), s.HiddenReasoning(), true, s.MedianOutputTokens()-s.MedianReasoningTokens()))
	}
	if s.CostConfigured() {
		fmt.Fprintf(w, "| cost/req | %s | | |\n", usd(s.Cost().P50))
	}
	if s.Failed() > 0 {
		fmt.Fprintf(w, "| errors | %s streams (%s) | | |\n", errorsCell(s), errorGroupsText(s))
	}
	fmt.Fprintln(w)
}

// formatMarkdownSingle renders the single-shot block as a two-column table.
func formatMarkdownSingle(w io.Writer, s bench.Summary, detail bool) {
	r := s.Results[0]
	fmt.Fprintf(w, "**tokps — %s @ %s**\n\n", s.Model, s.Host)
	fmt.Fprintln(w, "| metric | value |")
	fmt.Fprintln(w, "|---|---|")
	if r.PromptTokens >= 0 {
		fmt.Fprintf(w, "| prompt tokens | %d |\n", r.PromptTokens)
	} else {
		fmt.Fprintln(w, "| prompt tokens | n/a |")
	}
	fmt.Fprintf(w, "| output tokens | %d (%s) |\n", r.OutputTokens, exactLabel(r.TokensExact))
	if r.Reasoning {
		fmt.Fprintf(w, "| thinking tokens | %d (%s) |\n", r.ReasoningTokens, thinkingNote(r.ReasoningExact, r.HiddenReasoning, false, r.AnswerTokens()))
	}
	if r.Streamed {
		fmt.Fprintf(w, "| time to first | %s |\n", dur(r.TTFT))
		if r.Reasoning && !r.HiddenReasoning {
			if r.TTFA > 0 {
				fmt.Fprintf(w, "| first answer | %s |\n", dur(r.TTFA))
			} else {
				fmt.Fprintln(w, "| first answer | not reached |")
			}
		}
		fmt.Fprintf(w, "| generation | %s |\n", dur(r.GenTime))
	} else {
		fmt.Fprintln(w, "| time to first | n/a |")
		fmt.Fprintln(w, "| generation | n/a |")
	}
	fmt.Fprintf(w, "| total wall | %s |\n", dur(r.TotalWall))
	if r.Cost > 0 {
		fmt.Fprintf(w, "| cost | %s |\n", usd(r.Cost))
	}
	fmt.Fprintf(w, "| TPS (generation) | %.1f tok/s |\n", r.TPS())
	fmt.Fprintf(w, "| end-to-end | %.1f tok/s |\n", r.EndToEndTPS())
	if detail {
		if p50, p95, ok := s.ITL(); ok {
			fmt.Fprintf(w, "| ITL | p50 %s, p95 %s |\n", ms(p50), ms(p95))
		}
	}
	fmt.Fprintln(w)
}

// FormatSweepMarkdown writes the throughput-vs-concurrency curve as a table.
func FormatSweepMarkdown(w io.Writer, sums []bench.Summary) {
	if len(sums) == 0 {
		return
	}
	fmt.Fprintf(w, "**tokps — %s @ %s** (sweep, %d runs, %d warmup)\n\n", sums[0].Model, sums[0].Host, sums[0].RunCount(), sums[0].Warmup)
	fmt.Fprintln(w, "| concurrency | aggregate tok/s (range) | TTFT p50 (range) | TPS p50/stream | errors |")
	fmt.Fprintln(w, "|---|---|---|---|---|")
	for _, s := range sums {
		if s.AllFailed() {
			fmt.Fprintf(w, "| %d | failed (%s) | | | |\n", s.Concurrency, errorGroupsText(s))
			continue
		}
		fmt.Fprintf(w, "| %d | %s | %s | %.1f | %s |\n", s.Concurrency, aggCell(s), ttftCell(s), s.GenTPS().P50, errorsCell(s))
	}
	fmt.Fprintln(w)
}

// FormatCompareMarkdown writes the model-vs-model table in markdown.
func FormatCompareMarkdown(w io.Writer, sums []bench.Summary) {
	if len(sums) == 0 {
		return
	}
	mixed := mixedHosts(sums)
	cost := anyCost(sums)
	answer := anyReasoning(sums)
	fmt.Fprintf(w, "**tokps — %s**\n\n", compareHeader(sums))

	cols := []string{firstColumn(mixed), "TTFT p50"}
	if answer {
		cols = append(cols, "answer p50")
	}
	cols = append(cols, "TPS p50 (range)", "e2e p50")
	if cost {
		cols = append(cols, "cost/req")
	}
	cols = append(cols, "errors")
	fmt.Fprintf(w, "| %s |\n", strings.Join(cols, " | "))
	fmt.Fprintf(w, "|%s\n", strings.Repeat("---|", len(cols)))

	for _, s := range sums {
		if s.AllFailed() {
			fmt.Fprintf(w, "| %s | failed (%s) |%s\n", rowName(s, mixed), errorGroupsText(s), strings.Repeat(" |", len(cols)-2))
			continue
		}
		cells := []string{rowName(s, mixed), ttftP50Cell(s)}
		if answer {
			cells = append(cells, answerCell(s))
		}
		cells = append(cells, tpsCell(s), fmt.Sprintf("%.1f", s.E2ETPS().P50))
		if cost {
			cells = append(cells, costCell(s))
		}
		cells = append(cells, errorsCell(s))
		fmt.Fprintf(w, "| %s |\n", strings.Join(cells, " | "))
	}
	fmt.Fprintln(w)
}
