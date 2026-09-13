package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/canergulay/tokps/internal/bench"
)

// exitGateFailed is returned when a --min-tps / --max-ttft threshold fails.
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
// prefixed with labelOf(s) when non-empty, and reports whether any failed.
func checkGates(gate bench.Gate, sums []bench.Summary, labelOf func(bench.Summary) string, stderr io.Writer) bool {
	failed := false
	for _, s := range sums {
		for _, msg := range gate.Check(s) {
			failed = true
			if l := labelOf(s); l != "" {
				fmt.Fprintf(stderr, "FAIL: %s %s\n", l, msg)
			} else {
				fmt.Fprintf(stderr, "FAIL: %s\n", msg)
			}
		}
	}
	return failed
}

func noLabel(bench.Summary) string { return "" }

func levelLabel(s bench.Summary) string { return fmt.Sprintf("c=%d", s.Concurrency) }
