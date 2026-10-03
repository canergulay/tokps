package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/canergulay/tokps/internal/bench"
	"github.com/canergulay/tokps/internal/report"
)

// exitGateFailed is returned when a --min-tps / --max-ttft / --max-error-rate
// threshold fails.
const exitGateFailed = 3

// reportErr prints a benchmark error and returns the exit code: 130 for an
// interruption (with how far the run got), 1 otherwise.
func reportErr(err error, stderr io.Writer, runs int) int {
	var ie *bench.InterruptedError
	if errors.As(err, &ie) {
		fmt.Fprintf(stderr, "interrupted: %d/%d measured runs completed\n", ie.Completed, runs)
		return 130
	}
	fmt.Fprintf(stderr, "error: %v\n", err)
	return 1
}

// checkGates prints one FAIL line per violated threshold across sums,
// prefixed with labelOf(i) when non-empty, and reports whether any failed.
func checkGates(gate bench.Gate, sums []bench.Summary, labelOf func(i int) string, stderr io.Writer) bool {
	failed := false
	for i, s := range sums {
		for _, msg := range gate.Check(s) {
			failed = true
			if l := labelOf(i); l != "" {
				fmt.Fprintf(stderr, "FAIL: %s %s\n", l, msg)
			} else {
				fmt.Fprintf(stderr, "FAIL: %s\n", msg)
			}
		}
	}
	return failed
}

// writeSummary renders one benchmark in the selected format.
func writeSummary(w io.Writer, s bench.Summary, opts *options) error {
	switch {
	case opts.jsonOut:
		return report.FormatJSON(w, s)
	case opts.md:
		report.FormatMarkdown(w, s, opts.detail)
	default:
		report.FormatSummary(w, s, opts.detail)
	}
	return nil
}

// writeSweep renders the concurrency curve in the selected format.
func writeSweep(w io.Writer, sums []bench.Summary, opts *options) error {
	switch {
	case opts.jsonOut:
		return report.FormatSweepJSON(w, sums)
	case opts.md:
		report.FormatSweepMarkdown(w, sums)
	default:
		report.FormatSweep(w, sums)
	}
	return nil
}

// writeCompare renders the model comparison in the selected format.
func writeCompare(w io.Writer, sums []bench.Summary, opts *options) error {
	switch {
	case opts.jsonOut:
		return report.FormatCompareJSON(w, sums)
	case opts.md:
		report.FormatCompareMarkdown(w, sums)
	default:
		report.FormatCompare(w, sums)
	}
	return nil
}
