package bench

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"unicode/utf8"
)

// InterruptedError reports that a benchmark was cut short (e.g. by SIGINT)
// after some measured batches completed. Completed counts the timed batches
// that finished before cancellation.
type InterruptedError struct {
	Completed int
}

func (e *InterruptedError) Error() string {
	return fmt.Sprintf("interrupted after %d measured batch(es)", e.Completed)
}

// isInterrupted reports whether err wraps an *InterruptedError.
func isInterrupted(err error) bool {
	var ie *InterruptedError
	return errors.As(err, &ie)
}

// HTTPError is returned by Run when the endpoint answers with a non-2xx
// status. StatusText is the full status line (e.g. "429 Too Many Requests")
// and Body the trimmed, size-capped response body providers use to explain
// the rejection.
type HTTPError struct {
	Status     int
	StatusText string
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("endpoint returned %s: %s", e.StatusText, e.Body)
}

// StreamError records one failed stream. Batch is the 1-based measured batch
// it belonged to (0 for a warmup batch); Status is the HTTP status when the
// endpoint answered non-2xx, else 0.
type StreamError struct {
	Batch  int
	Status int
	Err    string
}

// maxErrRunes caps the error text stored per failed stream so JSON output
// stays bounded when providers return large HTML error pages.
const maxErrRunes = 512

// newStreamError captures err for the given batch, lifting the HTTP status
// out of an *HTTPError when there is one.
func newStreamError(batch int, err error) StreamError {
	se := StreamError{Batch: batch, Err: err.Error()}
	if utf8.RuneCountInString(se.Err) > maxErrRunes {
		se.Err = string([]rune(se.Err)[:maxErrRunes]) + "…"
	}
	var he *HTTPError
	if errors.As(err, &he) {
		se.Status = he.Status
	}
	return se
}

// maxLabelRunes caps free-text error labels in ErrorGroups.
const maxLabelRunes = 60

// label is the grouping key: "<status> <status text>" for HTTP rejections,
// otherwise the (truncated) error text.
func (e StreamError) label() string {
	if e.Status > 0 {
		if text := http.StatusText(e.Status); text != "" {
			return fmt.Sprintf("%d %s", e.Status, text)
		}
		return strconv.Itoa(e.Status)
	}
	if utf8.RuneCountInString(e.Err) <= maxLabelRunes {
		return e.Err
	}
	return string([]rune(e.Err)[:maxLabelRunes]) + "…"
}

// AllFailedError reports that every measured stream failed. RunN returns it
// together with the populated Summary so sweep and compare can still render
// the failed level or model.
type AllFailedError struct {
	Err error // the first stream error
}

func (e *AllFailedError) Error() string { return "all measured streams failed: " + e.Err.Error() }

// Unwrap exposes the first stream error to errors.As/Is.
func (e *AllFailedError) Unwrap() error { return e.Err }

// ErrorGroup counts stream failures that share a label.
type ErrorGroup struct {
	Label string
	Count int
}

// Failed returns the number of streams that errored.
func (s Summary) Failed() int { return len(s.Errors) }

// StreamCount returns the measured streams attempted. It falls back to
// successful + failed for summaries built without Streams (e.g. in tests).
func (s Summary) StreamCount() int {
	if s.Streams > 0 {
		return s.Streams
	}
	return len(s.Results) + len(s.Errors)
}

// ErrorRate is Failed ÷ StreamCount, or 0 when nothing was attempted.
func (s Summary) ErrorRate() float64 {
	if n := s.StreamCount(); n > 0 {
		return float64(len(s.Errors)) / float64(n)
	}
	return 0
}

// AllFailed reports whether the benchmark produced no successful stream —
// the warmup was rejected or every measured stream errored.
func (s Summary) AllFailed() bool { return len(s.Results) == 0 }

// ErrorGroups groups the recorded failures by label, most frequent first
// (ties broken by label) — e.g. "429 Too Many Requests ×6, 503 ... ×2".
func (s Summary) ErrorGroups() []ErrorGroup {
	counts := map[string]int{}
	for _, e := range s.Errors {
		counts[e.label()]++
	}
	groups := make([]ErrorGroup, 0, len(counts))
	for l, c := range counts {
		groups = append(groups, ErrorGroup{Label: l, Count: c})
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].Label < groups[j].Label
	})
	return groups
}
