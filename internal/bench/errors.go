package bench

import "fmt"

// InterruptedError reports that a benchmark was cut short (e.g. by SIGINT)
// after some measured batches completed. Completed counts the timed batches
// that finished before cancellation.
type InterruptedError struct {
	Completed int
}

func (e *InterruptedError) Error() string {
	return fmt.Sprintf("interrupted after %d measured batch(es)", e.Completed)
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
