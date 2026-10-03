package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestParseTarget(t *testing.T) {
	cases := []struct {
		in                 string
		model, url, keyEnv string
	}{
		{"gpt-4o-mini", "gpt-4o-mini", "", ""},
		{"deepseek-chat@https://api.deepseek.com", "deepseek-chat", "https://api.deepseek.com", ""},
		{"llama@http://localhost:8000/v1#LOCAL_KEY", "llama", "http://localhost:8000/v1", "LOCAL_KEY"},
		{"m#K", "m", "", "K"},
	}
	for _, c := range cases {
		got, err := parseTarget(c.in)
		if err != nil {
			t.Errorf("parseTarget(%q) error: %v", c.in, err)
			continue
		}
		if got.model != c.model || got.url != c.url || got.keyEnv != c.keyEnv {
			t.Errorf("parseTarget(%q) = %+v, want {%s %s %s}", c.in, got, c.model, c.url, c.keyEnv)
		}
	}
	for _, bad := range []string{"", "@http://x", "m@http://x#"} {
		if _, err := parseTarget(bad); err == nil {
			t.Errorf("parseTarget(%q): expected an error", bad)
		}
	}
}

func TestResolveKeyPrecedence(t *testing.T) {
	env := envMap(map[string]string{
		"DEEPSEEK_API_KEY": "ds", "API_KEY": "generic", "OPENAI_API_KEY": "oai", "MINE": "mine",
	})
	cases := []struct {
		explicit, flag, url, want string
	}{
		{"MINE", "flag", "https://api.deepseek.com", "mine"}, // #VAR beats everything
		{"", "flag", "https://api.deepseek.com", "flag"},     // then --api-key
		{"", "", "https://api.deepseek.com/v1", "ds"},        // then the provider's own variable
		{"", "", "https://API.DeepSeek.com", "ds"},           // host match ignores case
		{"", "", "http://localhost:8000/v1", "generic"},      // then API_KEY
		{"", "", "https://api.openai.com/v1", "oai"},         // OPENAI_API_KEY is OpenAI's own
	}
	for _, c := range cases {
		got, err := resolveKey(c.explicit, c.flag, c.url, env)
		if err != nil || got != c.want {
			t.Errorf("resolveKey(%q,%q,%q) = %q,%v want %q", c.explicit, c.flag, c.url, got, err, c.want)
		}
	}
	if got, _ := resolveKey("", "", "http://x", envMap(map[string]string{"OPENAI_API_KEY": "oai"})); got != "oai" {
		t.Errorf("last resort = %q, want OPENAI_API_KEY", got)
	}
	if _, err := resolveKey("NOPE", "", "http://x", env); err == nil || !strings.Contains(err.Error(), "$NOPE") {
		t.Errorf("unset explicit variable: err = %v, want it named", err)
	}
}

func TestRateValue(t *testing.T) {
	for in, want := range map[string]float64{"0.05": 0.05, "5%": 0.05, "0": 0, "100%": 1} {
		var r rateValue
		if err := r.Set(in); err != nil || r.v == nil || *r.v != want {
			t.Errorf("Set(%q) = %v,%v want %v", in, r.v, err, want)
		}
	}
	for _, bad := range []string{"x", "-0.1", "1.5", "150%"} {
		var r rateValue
		if err := r.Set(bad); err == nil {
			t.Errorf("Set(%q): expected an error", bad)
		}
	}
}

func TestRunRejectsTargetWithModel(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--model=a", "--target=b@http://x"}, &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), "--target and --model cannot be combined") {
		t.Errorf("exit=%d stderr=%q, want a usage error", code, errb.String())
	}
}

func TestRunRejectsTargetWithoutURL(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--target=a", "--target=b@http://x"}, &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), "--url is required") {
		t.Errorf("exit=%d stderr=%q, want a missing-URL usage error", code, errb.String())
	}
}

// authServer streams a completion and records each request's bearer token.
func authServer(t *testing.T, mu *sync.Mutex, seen *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*seen = append(*seen, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{}}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func TestRunCrossTargetCompareEndToEnd(t *testing.T) {
	var mu sync.Mutex
	var seenA, seenB []string
	a := authServer(t, &mu, &seenA)
	defer a.Close()
	b := authServer(t, &mu, &seenB)
	defer b.Close()
	t.Setenv("KEY_A", "secret-a")
	t.Setenv("API_KEY", "fallback")

	var out, errb bytes.Buffer
	code := run([]string{
		"--target=alpha@" + a.URL + "#KEY_A", "--target=beta@" + b.URL,
		"--runs=1", "--warmup=0", "--max-error-rate=0",
	}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
	}
	if strings.Join(seenA, ",") != "Bearer secret-a" || strings.Join(seenB, ",") != "Bearer fallback" {
		t.Errorf("keys: a=%v b=%v", seenA, seenB)
	}
	for _, want := range []string{"target", "alpha @ 127.0.0.1", "beta @ 127.0.0.1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunMaxErrorRateGateExits3(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1)%2 == 0 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer ts.Close()

	var out, errb bytes.Buffer
	code := run([]string{"--url=" + ts.URL, "--model=m", "--runs=4", "--warmup=0", "--max-error-rate=10%"}, &out, &errb)
	if code != 3 {
		t.Fatalf("exit = %d, want 3; stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "FAIL: error rate 50.0% (2/4 streams) > max 10.0%") {
		t.Errorf("stderr = %q, want the error-rate FAIL line", errb.String())
	}
}

func TestRunRejectsBadMaxErrorRate(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--url=http://x", "--model=m", "--max-error-rate=2"}, &out, &errb); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}
