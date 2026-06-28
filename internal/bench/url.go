package bench

import (
	"net/url"
	"strings"
)

// endpoint normalizes a configured base URL into a full chat/completions URL.
func endpoint(raw string) string {
	u := strings.TrimRight(raw, "/")
	if strings.HasSuffix(u, "/chat/completions") {
		return u
	}
	return u + "/chat/completions"
}

// hostOf returns the host portion of raw for display, or raw if it cannot
// be parsed.
func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}
