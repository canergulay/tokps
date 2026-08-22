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
// be parsed. Scheme-less URLs (e.g. "localhost:8000/v1") are parsed as
// http:// so their host is reported instead of the whole string.
func hostOf(raw string) string {
	s := raw
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}
