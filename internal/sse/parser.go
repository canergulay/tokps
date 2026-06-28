// Package sse extracts data payloads from a Server-Sent Events stream.
package sse

import (
	"bufio"
	"io"
	"strings"
)

// Scanner reads SSE "data:" payloads from a reader, one at a time.
type Scanner struct {
	sc   *bufio.Scanner
	next string
	done bool
}

// NewScanner returns a Scanner reading SSE events from r.
func NewScanner(r io.Reader) *Scanner {
	sc := bufio.NewScanner(r)
	// Allow long lines (large JSON chunks): up to 1 MiB.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &Scanner{sc: sc}
}

// Scan advances to the next data payload. It returns false at end of stream
// or once a "[DONE]" sentinel is seen.
func (s *Scanner) Scan() bool {
	if s.done {
		return false
	}
	for s.sc.Scan() {
		line := s.sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue // skip blank lines, comments, "event:" lines, garbage
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			s.done = true
			return false
		}
		if data == "" {
			continue
		}
		s.next = data
		return true
	}
	return false
}

// Data returns the current payload (text after "data:").
func (s *Scanner) Data() string { return s.next }

// Err returns the first non-EOF error encountered while reading.
func (s *Scanner) Err() error { return s.sc.Err() }
