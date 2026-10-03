package report

import (
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/canergulay/tokps/internal/bench"
)

// compareHeader renders the "compare @ host (…)" title line shared by the
// text and markdown tables.
func compareHeader(sums []bench.Summary) string {
	first := sums[0]
	h := "compare  ("
	if !mixedHosts(sums) {
		h = fmt.Sprintf("compare @ %s  (", first.Host)
	}
	if first.Concurrency > 1 {
		h += fmt.Sprintf("concurrency %d, ", first.Concurrency)
	}
	return h + fmt.Sprintf("%d runs, %d warmup)", first.RunCount(), first.Warmup)
}

// mixedHosts reports whether the compared summaries span several hosts.
func mixedHosts(sums []bench.Summary) bool {
	for _, s := range sums {
		if s.Host != sums[0].Host {
			return true
		}
	}
	return false
}

// rowName names a compare row: the model, plus its host when targets span
// several providers.
func rowName(s bench.Summary, mixed bool) string {
	if mixed {
		return s.Model + " @ " + s.Host
	}
	return s.Model
}

// firstColumn is the compare table's first column header.
func firstColumn(mixed bool) string {
	if mixed {
		return "target"
	}
	return "model"
}

// modelWidth returns the column width that fits every row name.
func modelWidth(sums []bench.Summary, mixed bool) int {
	width := len(firstColumn(mixed))
	for _, s := range sums {
		width = max(width, utf8.RuneCountInString(rowName(s, mixed)))
	}
	return width
}

// anyReasoning reports whether any compared model streamed its thinking, so
// the table needs a time-to-first-answer column.
func anyReasoning(sums []bench.Summary) bool {
	for _, s := range sums {
		if streamsThinking(s) {
			return true
		}
	}
	return false
}

// streamsThinking reports whether s streamed visible thinking, the only case
// where a separate time-to-first-answer exists.
func streamsThinking(s bench.Summary) bool {
	return s.Streamed() && s.Reasoning() && !s.HiddenReasoning()
}

// answerCell renders the time-to-first-answer median, "–" for a model that
// does not think out loud, or "not reached".
func answerCell(s bench.Summary) string {
	if !streamsThinking(s) {
		return "–"
	}
	a, reached := s.TTFA()
	if reached == 0 {
		return "not reached"
	}
	return secs(a.P50)
}

// FormatCompare writes the model-vs-model table, one row per model in input
// order. A model with no successful stream renders as a failed row.
func FormatCompare(w io.Writer, sums []bench.Summary) {
	if len(sums) == 0 {
		return
	}
	fmt.Fprintf(w, "\ntokps — %s\n\n", compareHeader(sums))
	mixed := mixedHosts(sums)
	width := modelWidth(sums, mixed)
	cost := anyCost(sums)
	answer := anyReasoning(sums)

	fmt.Fprintf(w, "  %-*s   %-10s", width, firstColumn(mixed), "TTFT p50")
	if answer {
		fmt.Fprintf(w, "   %-11s", "answer p50")
	}
	fmt.Fprintf(w, "   %-22s   %-9s", "TPS p50 (range)", "e2e p50")
	if cost {
		fmt.Fprintf(w, "   %-10s", "cost/req")
	}
	fmt.Fprintf(w, "   %s\n", "errors")

	for _, s := range sums {
		if s.AllFailed() {
			fmt.Fprintf(w, "  %-*s   failed (%s)\n", width, rowName(s, mixed), errorGroupsText(s))
			continue
		}
		fmt.Fprintf(w, "  %-*s   %-10s", width, rowName(s, mixed), ttftP50Cell(s))
		if answer {
			fmt.Fprintf(w, "   %-11s", answerCell(s))
		}
		fmt.Fprintf(w, "   %-22s   %-9.1f", tpsCell(s), s.E2ETPS().P50)
		if cost {
			fmt.Fprintf(w, "   %-10s", costCell(s))
		}
		fmt.Fprintf(w, "   %s\n", errorsCell(s))
	}
	fmt.Fprintln(w)
}

// FormatCompareJSON writes one --json-shaped object per model as a JSON array.
func FormatCompareJSON(w io.Writer, sums []bench.Summary) error {
	arr := make([]summaryJSON, 0, len(sums))
	for _, s := range sums {
		arr = append(arr, toJSON(s))
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(arr)
}
