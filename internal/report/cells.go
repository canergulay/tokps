package report

import (
	"fmt"
	"strings"

	"github.com/canergulay/tokps/internal/bench"
)

// Cell formatters shared by the text, markdown, sweep, and compare tables so
// every view renders a metric the same way.

// tpsCell renders the generation rate as "73.1 (69.8–75.4)".
func tpsCell(s bench.Summary) string {
	g := s.GenTPS()
	return fmt.Sprintf("%.1f (%.1f–%.1f)", g.P50, g.Min, g.Max)
}

// ttftCell renders TTFT as "2.61s (2.41s–2.95s)", or "n/a" without streaming.
func ttftCell(s bench.Summary) string {
	if !s.Streamed() {
		return "n/a"
	}
	t := s.TTFT()
	return fmt.Sprintf("%s (%s–%s)", secs(t.P50), secs(t.Min), secs(t.Max))
}

// ttftP50Cell renders only the TTFT median, or "n/a" without streaming.
func ttftP50Cell(s bench.Summary) string {
	if !s.Streamed() {
		return "n/a"
	}
	return secs(s.TTFT().P50)
}

// aggCell renders the aggregate rate as "250.0 (240.0–260.0)".
func aggCell(s bench.Summary) string {
	a := s.AggregateTPS()
	return fmt.Sprintf("%.1f (%.1f–%.1f)", a.P50, a.Min, a.Max)
}

// costCell renders the median per-request cost, or "–" when no price is set.
func costCell(s bench.Summary) string {
	if !s.CostConfigured() {
		return "–"
	}
	return usd(s.Cost().P50)
}

// errorsCell renders "–" when nothing failed, otherwise "failed/attempted".
func errorsCell(s bench.Summary) string {
	if s.Failed() == 0 {
		return "–"
	}
	return fmt.Sprintf("%d/%d", s.Failed(), s.StreamCount())
}

// errorGroupsText renders "429 Too Many Requests ×2, 503 Service Unavailable ×1".
func errorGroupsText(s bench.Summary) string {
	groups := s.ErrorGroups()
	parts := make([]string, len(groups))
	for i, g := range groups {
		parts[i] = fmt.Sprintf("%s ×%d", g.Label, g.Count)
	}
	return strings.Join(parts, ", ")
}

// exactLabel names the token-count source.
func exactLabel(exact bool) string {
	if exact {
		return "exact"
	}
	return "estimated"
}

// anyCost reports whether any summary has a configured cost.
func anyCost(sums []bench.Summary) bool {
	for _, s := range sums {
		if s.CostConfigured() {
			return true
		}
	}
	return false
}
