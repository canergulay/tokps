# tokencounter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a single-shot Go CLI that sends a test prompt to any OpenAI-compatible `/chat/completions` endpoint and reports token-generation throughput (TPS), TTFT, and token counts.

**Architecture:** A thin `main.go` parses flags and wires three small internal packages: `internal/sse` (turns an SSE byte stream into data payloads), `internal/bench` (builds/sends the request, drives the parser, collects timing + token metrics into a `Result`), and `internal/report` (formats the summary). Token counts come from the stream's `usage` field when present (exact) and fall back to counting content chunks (estimated).

**Tech Stack:** Go 1.23, standard library only (`net/http`, `encoding/json`, `bufio`, `flag`, `net/http/httptest` for tests). No third-party dependencies.

## Global Constraints

- Go version floor: **1.23** (matches installed toolchain).
- **Standard library only** — no third-party modules. `go.mod` must list zero `require` dependencies.
- Module path: **`tokencounter`**. Internal imports use `tokencounter/internal/...`.
- All packages under `internal/` so nothing is importable externally.
- TDD throughout: failing test first, minimal implementation, passing test, commit.
- Commit after every task with a `feat:`/`test:` style message.

---

### Task 1: Module init + SSE scanner

**Files:**
- Create: `go.mod`
- Create: `internal/sse/parser.go`
- Test: `internal/sse/parser_test.go`

**Interfaces:**
- Consumes: nothing (first task).
- Produces:
  - `sse.NewScanner(r io.Reader) *sse.Scanner`
  - `(*sse.Scanner) Scan() bool` — advances to next `data:` payload; returns false at end of stream or after a `[DONE]` sentinel.
  - `(*sse.Scanner) Data() string` — the current payload (text after `data:`, trimmed).
  - `(*sse.Scanner) Err() error` — underlying read error, if any.

- [ ] **Step 1: Initialize the module**

Run:
```bash
cd /Users/canergulay/code/indie/tokencounter
go mod init tokencounter
```
Expected: creates `go.mod` containing `module tokencounter` and a `go 1.23` line.

- [ ] **Step 2: Write the failing test**

Create `internal/sse/parser_test.go`:
```go
package sse

import (
	"reflect"
	"strings"
	"testing"
)

func TestScannerYieldsDataPayloads(t *testing.T) {
	input := "data: {\"a\":1}\n\ndata: {\"b\":2}\n\ndata: [DONE]\n\n"
	sc := NewScanner(strings.NewReader(input))

	var got []string
	for sc.Scan() {
		got = append(got, sc.Data())
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{`{"a":1}`, `{"b":2}`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestScannerSkipsNonDataAndBlankLines(t *testing.T) {
	input := ": comment\nevent: message\n\ndata: {\"x\":1}\n\ngarbage line\n\ndata: {\"y\":2}\n\n"
	sc := NewScanner(strings.NewReader(input))

	var got []string
	for sc.Scan() {
		got = append(got, sc.Data())
	}

	want := []string{`{"x":1}`, `{"y":2}`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestScannerStopsAtDone(t *testing.T) {
	input := "data: {\"a\":1}\n\ndata: [DONE]\n\ndata: {\"never\":1}\n\n"
	sc := NewScanner(strings.NewReader(input))

	var got []string
	for sc.Scan() {
		got = append(got, sc.Data())
	}

	want := []string{`{"a":1}`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/sse/`
Expected: FAIL — `undefined: NewScanner`.

- [ ] **Step 4: Write the minimal implementation**

Create `internal/sse/parser.go`:
```go
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
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/sse/`
Expected: PASS (`ok  	tokencounter/internal/sse`).

- [ ] **Step 6: Commit**

```bash
git add go.mod internal/sse/
git commit -m "feat: add SSE data-payload scanner"
```

---

### Task 2: Result metrics + TPS math

**Files:**
- Create: `internal/bench/metrics.go`
- Test: `internal/bench/metrics_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type bench.Result struct` with fields: `Model string`, `Host string`, `PromptTokens int` (−1 if unknown), `OutputTokens int`, `TokensExact bool`, `TTFT time.Duration`, `GenTime time.Duration`, `TotalWall time.Duration`, `Streamed bool`.
  - `(bench.Result) TPS() float64` — headline generation rate; `OutputTokens / GenTime`, falling back to `EndToEndTPS()` when `GenTime <= 0`.
  - `(bench.Result) EndToEndTPS() float64` — `OutputTokens / TotalWall`; 0 when `TotalWall <= 0`.

- [ ] **Step 1: Write the failing test**

Create `internal/bench/metrics_test.go`:
```go
package bench

import (
	"testing"
	"time"
)

func TestTPSUsesGenerationTime(t *testing.T) {
	r := Result{OutputTokens: 100, GenTime: 2 * time.Second, TotalWall: 4 * time.Second}
	if got := r.TPS(); got != 50 {
		t.Fatalf("TPS = %v, want 50", got)
	}
}

func TestTPSFallsBackToEndToEndWhenNoGenTime(t *testing.T) {
	r := Result{OutputTokens: 100, GenTime: 0, TotalWall: 4 * time.Second}
	if got := r.TPS(); got != 25 {
		t.Fatalf("TPS = %v, want 25", got)
	}
}

func TestEndToEndTPSZeroWallIsZero(t *testing.T) {
	r := Result{OutputTokens: 100, GenTime: 0, TotalWall: 0}
	if got := r.TPS(); got != 0 {
		t.Fatalf("TPS = %v, want 0", got)
	}
	if got := r.EndToEndTPS(); got != 0 {
		t.Fatalf("EndToEndTPS = %v, want 0", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/bench/`
Expected: FAIL — `undefined: Result`.

- [ ] **Step 3: Write the minimal implementation**

Create `internal/bench/metrics.go`:
```go
package bench

import "time"

// Result holds the outcome of a single benchmark run.
type Result struct {
	Model        string
	Host         string
	PromptTokens int  // -1 when unknown
	OutputTokens int
	TokensExact  bool // true when from usage, false when estimated from chunks
	TTFT         time.Duration
	GenTime      time.Duration // last content token - first content token
	TotalWall    time.Duration
	Streamed     bool // false when the non-streaming fallback was used
}

// TPS is the headline generation rate (output tokens per second of
// generation time). It falls back to the end-to-end rate when generation
// time is unavailable.
func (r Result) TPS() float64 {
	if r.GenTime > 0 {
		return float64(r.OutputTokens) / r.GenTime.Seconds()
	}
	return r.EndToEndTPS()
}

// EndToEndTPS is output tokens divided by total wall time.
func (r Result) EndToEndTPS() float64 {
	if r.TotalWall <= 0 {
		return 0
	}
	return float64(r.OutputTokens) / r.TotalWall.Seconds()
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/bench/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bench/metrics.go internal/bench/metrics_test.go
git commit -m "feat: add Result metrics and TPS calculations"
```

---

### Task 3: URL + host helpers

**Files:**
- Create: `internal/bench/url.go`
- Test: `internal/bench/url_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `bench.endpoint(raw string) string` — normalizes a base URL to a full chat/completions URL (appends `/chat/completions` unless already present).
  - `bench.hostOf(raw string) string` — extracts the host for display, falling back to the raw string.

- [ ] **Step 1: Write the failing test**

Create `internal/bench/url_test.go`:
```go
package bench

import "testing"

func TestEndpointAppendsChatCompletions(t *testing.T) {
	cases := map[string]string{
		"https://api.openai.com/v1":                  "https://api.openai.com/v1/chat/completions",
		"https://api.openai.com/v1/":                 "https://api.openai.com/v1/chat/completions",
		"https://open.bigmodel.cn/api/paas/v4":       "https://open.bigmodel.cn/api/paas/v4/chat/completions",
		"https://x.test/v1/chat/completions":         "https://x.test/v1/chat/completions",
		"https://x.test/v1/chat/completions/":        "https://x.test/v1/chat/completions",
	}
	for in, want := range cases {
		if got := endpoint(in); got != want {
			t.Errorf("endpoint(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHostOf(t *testing.T) {
	if got := hostOf("https://open.bigmodel.cn/api/paas/v4"); got != "open.bigmodel.cn" {
		t.Errorf("hostOf = %q, want open.bigmodel.cn", got)
	}
	if got := hostOf("not a url"); got != "not a url" {
		t.Errorf("hostOf fallback = %q, want raw string", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/bench/ -run 'Endpoint|HostOf'`
Expected: FAIL — `undefined: endpoint`.

- [ ] **Step 3: Write the minimal implementation**

Create `internal/bench/url.go`:
```go
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/bench/ -run 'Endpoint|HostOf'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bench/url.go internal/bench/url_test.go
git commit -m "feat: add endpoint normalization and host helpers"
```

---

### Task 4: bench.Run — streaming happy path + no-usage fallback

**Files:**
- Create: `internal/bench/bench.go`
- Test: `internal/bench/bench_test.go`

**Interfaces:**
- Consumes: `sse.NewScanner` (Task 1); `Result` (Task 2); `endpoint`, `hostOf` (Task 3).
- Produces:
  - `type bench.Config struct { URL, Model, APIKey, Prompt string; MaxTokens int; Timeout time.Duration; Client *http.Client; Now func() time.Time }` — `Client` defaults to `&http.Client{}` when nil; `Now` defaults to `time.Now` when nil.
  - `bench.Run(ctx context.Context, cfg Config) (Result, error)` — sends the streaming request and returns metrics.

- [ ] **Step 1: Write the failing test**

Create `internal/bench/bench_test.go`:
```go
package bench

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/bench/ -run Streaming`
Expected: FAIL — `undefined: Run` / `undefined: Config`.

- [ ] **Step 3: Write the minimal implementation**

Create `internal/bench/bench.go`:
```go
package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"tokencounter/internal/sse"
)

// Config controls a single benchmark run.
type Config struct {
	URL       string
	Model     string
	APIKey    string
	Prompt    string
	MaxTokens int
	Timeout   time.Duration
	Client    *http.Client     // defaults to &http.Client{} when nil
	Now       func() time.Time // defaults to time.Now when nil
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatRequest struct {
	Model         string        `json:"model"`
	Messages      []chatMessage `json:"messages"`
	MaxTokens     int           `json:"max_tokens"`
	Stream        bool          `json:"stream"`
	StreamOptions streamOptions `json:"stream_options"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *usage `json:"usage"`
}

// Run sends a streaming chat-completions request and returns timing and
// token-throughput metrics.
func Run(ctx context.Context, cfg Config) (Result, error) {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{}
	}

	body, err := json.Marshal(chatRequest{
		Model:         cfg.Model,
		Messages:      []chatMessage{{Role: "user", Content: cfg.Prompt}},
		MaxTokens:     cfg.MaxTokens,
		Stream:        true,
		StreamOptions: streamOptions{IncludeUsage: true},
	})
	if err != nil {
		return Result{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(cfg.URL), bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	host := hostOf(cfg.URL)

	tSend := now()
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return Result{}, fmt.Errorf("endpoint returned %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return runNonStreaming(resp, cfg, host, tSend, now)
	}
	return runStreaming(resp, cfg, host, tSend, now)
}

func runStreaming(resp *http.Response, cfg Config, host string, tSend time.Time, now func() time.Time) (Result, error) {
	res := Result{Model: cfg.Model, Host: host, PromptTokens: -1, Streamed: true}

	var tFirst, tLast time.Time
	chunkCount := 0
	var u *usage

	sc := sse.NewScanner(resp.Body)
	for sc.Scan() {
		var chunk streamChunk
		if err := json.Unmarshal([]byte(sc.Data()), &chunk); err != nil {
			continue // skip malformed JSON
		}
		if chunk.Usage != nil {
			u = chunk.Usage
		}
		content := ""
		if len(chunk.Choices) > 0 {
			content = chunk.Choices[0].Delta.Content
		}
		if content != "" {
			t := now()
			if tFirst.IsZero() {
				tFirst = t
			}
			tLast = t
			chunkCount++
		}
	}
	if err := sc.Err(); err != nil {
		return Result{}, err
	}
	tEnd := now()

	if u != nil {
		res.PromptTokens = u.PromptTokens
		res.OutputTokens = u.CompletionTokens
		res.TokensExact = true
	} else {
		res.OutputTokens = chunkCount
		res.TokensExact = false
	}
	if !tFirst.IsZero() {
		res.TTFT = tFirst.Sub(tSend)
		res.GenTime = tLast.Sub(tFirst)
		res.TotalWall = tLast.Sub(tSend)
	} else {
		res.TotalWall = tEnd.Sub(tSend)
	}
	return res, nil
}
```

Note: `runNonStreaming` is referenced here but implemented in Task 5. To keep this task compiling and testable on its own, add a temporary stub at the bottom of `bench.go` now; Task 5 replaces it:
```go
func runNonStreaming(resp *http.Response, cfg Config, host string, tSend time.Time, now func() time.Time) (Result, error) {
	return Result{}, fmt.Errorf("non-streaming response not yet supported")
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/bench/ -run Streaming`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bench/bench.go internal/bench/bench_test.go
git commit -m "feat: add streaming benchmark run with usage/chunk token counting"
```

---

### Task 5: bench.Run — error responses + non-streaming fallback

**Files:**
- Modify: `internal/bench/bench.go` (replace the `runNonStreaming` stub)
- Modify: `internal/bench/bench_test.go` (add cases)

**Interfaces:**
- Consumes: everything from Task 4.
- Produces: `runNonStreaming` now parses a complete chat-completions JSON response and fills `Result` (uses `usage` when present; otherwise estimates output tokens from whitespace-separated words of the message content; sets `Streamed=false`, leaves `TTFT`/`GenTime` zero).

- [ ] **Step 1: Write the failing tests**

Add to `internal/bench/bench_test.go`:
```go
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
```

Add the `strings` import to the test file if not already present (it is used by the error test):
```go
import "strings"
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/bench/ -run 'Non2xx|NonStreaming'`
Expected: the non-streaming test FAILS (`non-streaming response not yet supported`). The non-2xx test should already pass — that is fine.

- [ ] **Step 3: Replace the stub with the real implementation**

In `internal/bench/bench.go`, replace the temporary `runNonStreaming` stub with:
```go
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage *usage `json:"usage"`
}

func runNonStreaming(resp *http.Response, cfg Config, host string, tSend time.Time, now func() time.Time) (Result, error) {
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return Result{}, err
	}
	tEnd := now()

	var cr chatResponse
	if err := json.Unmarshal(b, &cr); err != nil {
		return Result{}, fmt.Errorf("could not parse response: %w", err)
	}

	res := Result{
		Model:        cfg.Model,
		Host:         host,
		PromptTokens: -1,
		Streamed:     false,
		TotalWall:    tEnd.Sub(tSend),
	}
	if cr.Usage != nil {
		res.PromptTokens = cr.Usage.PromptTokens
		res.OutputTokens = cr.Usage.CompletionTokens
		res.TokensExact = true
	} else {
		content := ""
		if len(cr.Choices) > 0 {
			content = cr.Choices[0].Message.Content
		}
		res.OutputTokens = len(strings.Fields(content))
		res.TokensExact = false
	}
	return res, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/bench/`
Expected: PASS (all bench tests).

- [ ] **Step 5: Commit**

```bash
git add internal/bench/bench.go internal/bench/bench_test.go
git commit -m "feat: handle error responses and non-streaming fallback"
```

---

### Task 6: report formatting

**Files:**
- Create: `internal/report/report.go`
- Test: `internal/report/report_test.go`

**Interfaces:**
- Consumes: `bench.Result`, `(Result).TPS()`, `(Result).EndToEndTPS()`.
- Produces: `report.Format(w io.Writer, r bench.Result)` — writes the human-readable summary block.

- [ ] **Step 1: Write the failing test**

Create `internal/report/report_test.go`:
```go
package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"tokencounter/internal/bench"
)

func TestFormatStreamingExact(t *testing.T) {
	r := bench.Result{
		Model: "glm-4-flash", Host: "open.bigmodel.cn",
		PromptTokens: 14, OutputTokens: 487, TokensExact: true,
		TTFT: 420 * time.Millisecond, GenTime: 6310 * time.Millisecond,
		TotalWall: 6730 * time.Millisecond, Streamed: true,
	}
	var buf bytes.Buffer
	Format(&buf, r)
	out := buf.String()

	for _, want := range []string{"glm-4-flash", "open.bigmodel.cn", "487", "(exact)", "TPS", "end-to-end"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatEstimatedAndNonStreaming(t *testing.T) {
	r := bench.Result{
		Model: "local", Host: "localhost:1234",
		PromptTokens: -1, OutputTokens: 100, TokensExact: false,
		TotalWall: 2 * time.Second, Streamed: false,
	}
	var buf bytes.Buffer
	Format(&buf, r)
	out := buf.String()

	if !strings.Contains(out, "(estimated)") {
		t.Errorf("expected (estimated) label:\n%s", out)
	}
	if !strings.Contains(out, "n/a") {
		t.Errorf("expected n/a for prompt tokens / timing:\n%s", out)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/report/`
Expected: FAIL — `undefined: Format`.

- [ ] **Step 3: Write the minimal implementation**

Create `internal/report/report.go`:
```go
// Package report formats benchmark results for the terminal.
package report

import (
	"fmt"
	"io"
	"time"

	"tokencounter/internal/bench"
)

// Format writes a human-readable summary of r to w.
func Format(w io.Writer, r bench.Result) {
	fmt.Fprintf(w, "\ntokencounter — %s @ %s\n\n", r.Model, r.Host)

	if r.PromptTokens >= 0 {
		fmt.Fprintf(w, "  prompt tokens     %d\n", r.PromptTokens)
	} else {
		fmt.Fprintf(w, "  prompt tokens     n/a\n")
	}

	label := "exact"
	if !r.TokensExact {
		label = "estimated"
	}
	fmt.Fprintf(w, "  output tokens     %d   (%s)\n", r.OutputTokens, label)

	if r.Streamed {
		fmt.Fprintf(w, "  time to first     %s\n", dur(r.TTFT))
		fmt.Fprintf(w, "  generation        %s\n", dur(r.GenTime))
	} else {
		fmt.Fprintf(w, "  time to first     n/a\n")
		fmt.Fprintf(w, "  generation        n/a\n")
	}
	fmt.Fprintf(w, "  total wall        %s\n\n", dur(r.TotalWall))

	fmt.Fprintf(w, "  TPS               %.1f tok/s   (generation)\n", r.TPS())
	fmt.Fprintf(w, "  end-to-end        %.1f tok/s   (incl. TTFT)\n\n", r.EndToEndTPS())
}

func dur(d time.Duration) string {
	return fmt.Sprintf("%.2f s", d.Seconds())
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/report/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/report/
git commit -m "feat: add result report formatting"
```

---

### Task 7: CLI wiring + build

**Files:**
- Create: `main.go`
- Create: `README.md`

**Interfaces:**
- Consumes: `bench.Config`, `bench.Run`, `report.Format`.
- Produces: the `tokencounter` binary entrypoint. (No unit test — thin wiring verified by `go build` and `go vet`; logic lives in tested packages.)

- [ ] **Step 1: Write main.go**

Create `main.go`:
```go
// Command tokencounter benchmarks the token-generation throughput of an
// OpenAI-compatible /chat/completions endpoint.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"tokencounter/internal/bench"
	"tokencounter/internal/report"
)

const defaultPrompt = "Write a detailed explanation of how TCP congestion control works, " +
	"covering slow start, congestion avoidance, fast retransmit, and fast recovery."

func main() {
	url := flag.String("url", "", "Base URL of the OpenAI-compatible endpoint (required)")
	model := flag.String("model", "", "Model name (required)")
	apiKey := flag.String("api-key", "", "API key (defaults to the OPENAI_API_KEY env var)")
	prompt := flag.String("prompt", defaultPrompt, "Test prompt to send")
	maxTokens := flag.Int("max-tokens", 512, "Maximum output tokens")
	timeout := flag.Duration("timeout", 60*time.Second, "Whole-request timeout")
	flag.Parse()

	if *url == "" || *model == "" {
		fmt.Fprintln(os.Stderr, "error: --url and --model are required\n")
		flag.Usage()
		os.Exit(2)
	}

	key := *apiKey
	if key == "" {
		key = os.Getenv("OPENAI_API_KEY")
	}

	cfg := bench.Config{
		URL:       *url,
		Model:     *model,
		APIKey:    key,
		Prompt:    *prompt,
		MaxTokens: *maxTokens,
		Timeout:   *timeout,
	}

	res, err := bench.Run(context.Background(), cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	report.Format(os.Stdout, res)
}
```

- [ ] **Step 2: Build and vet**

Run:
```bash
go build ./... && go vet ./...
```
Expected: no output, exit 0.

- [ ] **Step 3: Verify the flag guard and help**

Run:
```bash
go run . ; echo "exit=$?"
```
Expected: prints `error: --url and --model are required`, the usage text, and `exit=2`.

- [ ] **Step 4: Run the full test suite**

Run: `go test ./...`
Expected: all packages PASS (`ok` for `internal/sse`, `internal/bench`, `internal/report`; `no test files` for the root package is fine).

- [ ] **Step 5: Write README.md**

Create `README.md`:
```markdown
# tokencounter

Single-shot CLI that measures token-generation throughput (TPS) of any
OpenAI-compatible `/chat/completions` endpoint.

## Build

```sh
go build -o tokencounter .
```

## Usage

```sh
export OPENAI_API_KEY=sk-...
./tokencounter --url https://api.openai.com/v1 --model gpt-4o-mini
./tokencounter --url https://open.bigmodel.cn/api/paas/v4 --model glm-4-flash
```

| Flag | Default | Description |
|------|---------|-------------|
| `--url` | (required) | Base URL; `/chat/completions` is appended automatically. |
| `--model` | (required) | Model name. |
| `--api-key` | `OPENAI_API_KEY` env | Bearer token. |
| `--prompt` | built-in | Override the test prompt. |
| `--max-tokens` | 512 | Upper bound on output length. |
| `--timeout` | 60s | Request timeout. |

The headline **TPS** is output tokens ÷ generation time (excludes time to
first token); **end-to-end** includes TTFT. Token counts are taken from the
stream's `usage` field when present (labeled `exact`) and estimated from
streamed chunks otherwise (labeled `estimated`).
```

- [ ] **Step 6: Commit**

```bash
git add main.go README.md
git commit -m "feat: add CLI entrypoint and README"
```

---

## Self-Review notes

- **Spec coverage:** flags (Task 7), URL normalization incl. GLM (Task 3), streaming request with `stream_options.include_usage` (Task 4), exact-vs-estimated token counting (Tasks 4–5), TTFT/generation/end-to-end metrics (Tasks 2, 4), error responses + non-streaming fallback (Task 5), report block incl. `n/a`/`estimated` labels (Task 6), testing strategy via `httptest` (Tasks 4–5) — all mapped to tasks.
- **Type consistency:** `Result`, `Config`, `Run`, `endpoint`, `hostOf`, `usage`, `streamChunk`, `chatResponse`, `Format` names are used identically across tasks. `runNonStreaming` is introduced as a stub in Task 4 and replaced in Task 5 (called with the same signature).
- **No placeholders:** every code step contains complete, compilable code.
