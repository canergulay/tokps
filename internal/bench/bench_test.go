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

func TestRunStreamingEstimatesFromChunksWhenNoUsage(t *testing.T) {
	ts := sseServer(t, []string{
		`{"choices":[{"delta":{"content":"a"}}]}`,
		`{"choices":[{"delta":{"content":"b"}}]}`,
		`{"choices":[{"delta":{"content":"c"}}]}`,
	})
	defer ts.Close()

	res, err := Run(context.Background(), testConfig(ts.URL))
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.TokensExact {
		t.Errorf("TokensExact = true, want false")
	}
	if res.OutputTokens != 3 {
		t.Errorf("OutputTokens = %d, want 3 (one per content chunk)", res.OutputTokens)
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
