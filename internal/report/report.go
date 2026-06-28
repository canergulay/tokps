// Package report formats benchmark results for the terminal.
package report

import (
	"fmt"
	"io"
	"time"

	"tokencounter/internal/bench"
)

// Format writes a human-readable summary of r to w.
func Format(w io.Writer, r bench.Result) {
	fmt.Fprintf(w, "\ntokencounter — %s @ %s\n\n", r.Model, r.Host)

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

func dur(d time.Duration) string {
	return fmt.Sprintf("%.2f s", d.Seconds())
}
