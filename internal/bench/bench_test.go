package bench

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sseServer returns a test server that streams the given chunks as SSE
// followed by a [DONE] sentinel.
func sseServer(t *testing.T, chunks []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			if fl != nil {
				fl.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if fl != nil {
			fl.Flush()
		}
	}))
}

func testConfig(url string) Config {
	return Config{URL: url, Model: "test-model", Prompt: "hi", MaxTokens: 64, Timeout: 10 * time.Second}
}

func TestRunStreamingUsesUsageWhenPresent(t *testing.T) {
	ts := sseServer(t, []string{
		`{"choices":[{"delta":{"content":"Hello"}}]}`,
		`{"choices":[{"delta":{"content":" world"}}]}`,
		`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":12,"completion_tokens":42}}`,
	})
	defer ts.Close()

	res, err := Run(context.Background(), testConfig(ts.URL))
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !res.Streamed {
		t.Errorf("Streamed = false, want true")
	}
	if !res.TokensExact {
		t.Errorf("TokensExact = false, want true")
	}
	if res.OutputTokens != 42 {
		t.Errorf("OutputTokens = %d, want 42", res.OutputTokens)
	}
	if res.PromptTokens != 12 {
		t.Errorf("PromptTokens = %d, want 12", res.PromptTokens)
	}
}

func TestRunStreamingEstimatesFromCharsWhenNoUsage(t *testing.T) {
	// Two chunks but 24 runes of text. Counting chunks would say 2; the
	// chars-per-token estimate (24/4) says 6, independent of how the server
	// chose to chunk the stream.
	ts := sseServer(t, []string{
		`{"choices":[{"delta":{"content":"Hello world!"}}]}`,
		`{"choices":[{"delta":{"content":"Goodbye now!"}}]}`,
	})
	defer ts.Close()

	res, err := Run(context.Background(), testConfig(ts.URL))
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.TokensExact {
		t.Errorf("TokensExact = true, want false")
	}
	if res.OutputTokens != 6 {
		t.Errorf("OutputTokens = %d, want 6 (24 runes / 4)", res.OutputTokens)
	}
}

// fakeClock returns a now() function that advances by step on every call,
// starting at the Unix epoch. Deterministic timing for tests.
func fakeClock(step time.Duration) func() time.Time {
	base := time.Unix(0, 0)
	var n int64
	return func() time.Time {
		t := base.Add(time.Duration(n) * step)
		n++
		return t
	}
}

// GLM-5.2 and other reasoning models stream generated tokens in
// delta.reasoning_content rather than delta.content. Those tokens must count
// toward timing and throughput.
func TestRunStreamingCountsReasoningContentTiming(t *testing.T) {
	ts := sseServer(t, []string{
		`{"choices":[{"delta":{"reasoning_content":"a"}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"b"}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"c"}}]}`,
		`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":9,"completion_tokens":30}}`,
	})
	defer ts.Close()

	cfg := testConfig(ts.URL)
	cfg.Now = fakeClock(time.Second)

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !res.Streamed {
		t.Errorf("Streamed = false, want true")
	}
	if res.OutputTokens != 30 || !res.TokensExact {
		t.Errorf("got OutputTokens=%d exact=%v, want 30/true", res.OutputTokens, res.TokensExact)
	}
	// tSend=0s, first reasoning token at 1s, last at 3s.
	if res.TTFT != time.Second {
		t.Errorf("TTFT = %v, want 1s", res.TTFT)
	}
	if res.GenTime != 2*time.Second {
		t.Errorf("GenTime = %v, want 2s", res.GenTime)
	}
}

func TestRunStreamingReasoningNoUsageEstimatesFromChars(t *testing.T) {
	// Reasoning tokens count toward the estimate too: 16 runes / 4 = 4.
	ts := sseServer(t, []string{
		`{"choices":[{"delta":{"reasoning_content":"abcdefgh"}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"ijklmnop"}}]}`,
	})
	defer ts.Close()

	res, err := Run(context.Background(), testConfig(ts.URL))
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.TokensExact {
		t.Errorf("TokensExact = true, want false")
	}
	if res.OutputTokens != 4 {
		t.Errorf("OutputTokens = %d, want 4 (16 runes / 4)", res.OutputTokens)
	}
}

func TestRunReturnsErrorOnNon2xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	}))
	defer ts.Close()

	_, err := Run(context.Background(), testConfig(ts.URL))
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q, want it to mention 500", err.Error())
	}
}

func TestRunNonStreamingFallbackUsesUsage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"hello there"}}],"usage":{"prompt_tokens":5,"completion_tokens":7}}`)
	}))
	defer ts.Close()

	res, err := Run(context.Background(), testConfig(ts.URL))
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Streamed {
		t.Errorf("Streamed = true, want false")
	}
	if !res.TokensExact || res.OutputTokens != 7 || res.PromptTokens != 5 {
		t.Errorf("got OutputTokens=%d PromptTokens=%d exact=%v, want 7/5/true",
			res.OutputTokens, res.PromptTokens, res.TokensExact)
	}
}
