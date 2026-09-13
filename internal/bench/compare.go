package bench

import "errors"

// isInterrupted reports whether err wraps an *InterruptedError.
func isInterrupted(err error) bool {
	var ie *InterruptedError
	return errors.As(err, &ie)
}
