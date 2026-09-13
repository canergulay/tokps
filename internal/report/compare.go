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
	h := fmt.Sprintf("compare @ %s  (", first.Host)
	if first.Concurrency > 1 {
		h += fmt.Sprintf("concurrency %d, ", first.Concurrency)
	}
	return h + fmt.Sprintf("%d runs, %d warmup)", first.RunCount(), first.Warmup)
}

// modelWidth returns the column width that fits every model name.
func modelWidth(sums []bench.Summary) int {
	width := len("model")
	for _, s := range sums {
		width = max(width, utf8.RuneCountInString(s.Model))
	}
	return width
}

// FormatCompare writes the model-vs-model table, one row per model in input
// order. A model with no successful stream renders as a failed row.
func FormatCompare(w io.Writer, sums []bench.Summary) {
	if len(sums) == 0 {
		return
	}
	fmt.Fprintf(w, "\ntokps — %s\n\n", compareHeader(sums))
	width := modelWidth(sums)
	cost := anyCost(sums)

	fmt.Fprintf(w, "  %-*s   %-10s   %-22s   %-9s", width, "model", "TTFT p50", "TPS p50 (range)", "e2e p50")
	if cost {
		fmt.Fprintf(w, "   %-10s", "cost/req")
	}
	fmt.Fprintf(w, "   %s\n", "errors")

	for _, s := range sums {
		if s.AllFailed() {
			fmt.Fprintf(w, "  %-*s   failed (%s)\n", width, s.Model, errorGroupsText(s))
			continue
		}
		fmt.Fprintf(w, "  %-*s   %-10s   %-22s   %-9.1f", width, s.Model, ttftP50Cell(s), tpsCell(s), s.E2ETPS().P50)
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
