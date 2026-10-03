package bench

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/canergulay/tokps/internal/sse"
)

// avgCharsPerToken is the default chars/token ratio used to estimate token
// counts when the server omits a usage block. ~4 chars/token is OpenAI's
// documented heuristic for English text — model-agnostic and independent of
// how the server chose to chunk the stream. Override with Config.CharsPerToken
// for other scripts (CJK is closer to 1.5-2).
const avgCharsPerToken = 4

// estimateTokens approximates a token count from a rune count.
func estimateTokens(runes int, charsPerToken float64) int {
	if charsPerToken <= 0 {
		charsPerToken = avgCharsPerToken
	}
	return int(math.Round(float64(runes) / charsPerToken))
}

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
	// Warnf, when set, receives non-fatal diagnostics (e.g. SSE chunks that
	// could not be parsed). Nil silences them.
	Warnf func(format string, args ...any)

	// Progress, when set, is called after every warmup and measured batch
	// with a ProgressEvent. Nil disables progress reporting.
	Progress func(ProgressEvent)

	// MaxTokensField names the request field carrying the output cap:
	// "max_tokens" (default, OpenAI-compatible) or "max_completion_tokens"
	// (newer OpenAI models / some providers). When empty it defaults to
	// max_tokens. Run also retries once with max_completion_tokens when an
	// endpoint rejects max_tokens with a 400 mentioning the field.
	MaxTokensField string

	// CostIn and CostOut are USD per 1M tokens. When either is > 0, each
	// Result also carries an estimated per-request cost.
	CostIn  float64
	CostOut float64

	// CharsPerToken overrides the ~4 chars/token estimate used when a server
	// omits usage (CJK text is closer to 1.5-2).
	CharsPerToken float64

	// ExtraBody holds additional request fields merged into the JSON body
	// (e.g. {"temperature": 0} or provider-specific thinking toggles). Its
	// keys override the fields tokps sets.
	ExtraBody map[string]any
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatRequest struct {
	Model               string        `json:"model"`
	Messages            []chatMessage `json:"messages"`
	MaxTokens           int           `json:"max_tokens,omitempty"`
	MaxCompletionTokens int           `json:"max_completion_tokens,omitempty"`
	Stream              bool          `json:"stream"`
	StreamOptions       streamOptions `json:"stream_options"`
}

type usage struct {
	PromptTokens            int `json:"prompt_tokens"`
	CompletionTokens        int `json:"completion_tokens"`
	TotalTokens             int `json:"total_tokens"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// reasoningTokens returns the thinking-token count the server reported, and
// whether it reported one at all.
func (u *usage) reasoningTokens() (int, bool) {
	if u == nil || u.CompletionTokensDetails == nil {
		return 0, false
	}
	return u.CompletionTokensDetails.ReasoningTokens, true
}

// outputTokens returns every generated token. OpenAI and most providers count
// reasoning inside completion_tokens; xAI reports it on top (total = prompt +
// completion + reasoning), so it is added back there.
func (u *usage) outputTokens() int {
	n, _ := u.reasoningTokens()
	if n > 0 && (n > u.CompletionTokens || (u.TotalTokens > 0 && u.TotalTokens == u.PromptTokens+u.CompletionTokens+n)) {
		return u.CompletionTokens + n
	}
	return u.CompletionTokens
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
			// ReasoningContent carries thinking-mode tokens for reasoning
			// models (DeepSeek, GLM, Qwen); OpenRouter, Ollama and newer
			// vLLM name the same field Reasoning. These are generated
			// tokens and count toward throughput.
			ReasoningContent lenientString `json:"reasoning_content"`
			Reasoning        lenientString `json:"reasoning"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *usage `json:"usage"`
}

// lenientString decodes a JSON string and ignores any other type, so a
// provider that puts an object in a reasoning field cannot turn every chunk
// into a malformed one.
type lenientString string

func (s *lenientString) UnmarshalJSON(b []byte) error {
	var v string
	if json.Unmarshal(b, &v) == nil {
		*s = lenientString(v)
	}
	return nil
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage *usage `json:"usage"`
}

// poolingClient returns an HTTP client whose idle-connection pool is sized for
// `concurrency` parallel streams. http.DefaultTransport keeps only two idle
// connections per host, so under load warmup could not pre-establish every
// connection the measured runs reuse, and the measured batches would pay TCP/TLS
// setup that warmup is meant to absorb. Sizing the pool to the stream count
// keeps the documented steady-state guarantee true under concurrency.
func poolingClient(concurrency int) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 0 // no global cap; bound per-host instead
	t.MaxIdleConnsPerHost = concurrency
	// An interleaved compare leaves each target's warmed connections idle
	// while the other targets run; keep them longer than the default 90s.
	t.IdleConnTimeout = 5 * time.Minute
	return &http.Client{Transport: t}
}

// requestCost estimates the cost of a single request from the configured
// per-1M-token prices. It returns 0 when neither price is set.
func requestCost(cfg Config, promptTokens, outputTokens int) float64 {
	if cfg.CostIn <= 0 && cfg.CostOut <= 0 {
		return 0
	}
	cost := 0.0
	if promptTokens >= 0 {
		cost += float64(promptTokens) / 1e6 * cfg.CostIn
	}
	cost += float64(outputTokens) / 1e6 * cfg.CostOut
	return cost
}

// overlayJSON merges extra into the JSON object in base. Keys in extra win,
// so a user can override anything tokps sets (including stream).
func overlayJSON(base []byte, extra map[string]any) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(base, &m); err != nil {
		return nil, err
	}
	for k, v := range extra {
		m[k] = v
	}
	return json.Marshal(m)
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

	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	send := func(field string) (*http.Response, time.Time, error) {
		reqBody := chatRequest{
			Model:         cfg.Model,
			Messages:      []chatMessage{{Role: "user", Content: cfg.Prompt}},
			Stream:        true,
			StreamOptions: streamOptions{IncludeUsage: true},
		}
		if field == "max_completion_tokens" {
			reqBody.MaxCompletionTokens = cfg.MaxTokens
		} else {
			reqBody.MaxTokens = cfg.MaxTokens
		}

		body, err := json.Marshal(reqBody)
		if err != nil {
			return nil, time.Time{}, err
		}
		if len(cfg.ExtraBody) > 0 {
			if body, err = overlayJSON(body, cfg.ExtraBody); err != nil {
				return nil, time.Time{}, err
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(cfg.URL), bytes.NewReader(body))
		if err != nil {
			return nil, time.Time{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		if cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		}

		tSend := now()
		resp, err := client.Do(req)
		if err != nil {
			return nil, time.Time{}, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
			resp.Body.Close()
			return nil, time.Time{}, &HTTPError{
				Status:     resp.StatusCode,
				StatusText: resp.Status,
				Body:       strings.TrimSpace(string(b)),
			}
		}
		return resp, tSend, nil
	}

	field := cfg.MaxTokensField
	if field == "" {
		field = "max_tokens"
	}
	resp, tSend, err := send(field)
	if err != nil {
		// Newer OpenAI models reject max_tokens and ask for
		// max_completion_tokens. Retry once with the other field when the
		// endpoint says so.
		var he *HTTPError
		if field != "max_completion_tokens" && errors.As(err, &he) && strings.Contains(he.Body, "max_completion_tokens") {
			resp, tSend, err = send("max_completion_tokens")
		}
		if err != nil {
			return Result{}, err
		}
	}
	defer resp.Body.Close()

	host := hostOf(cfg.URL)

	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return runNonStreaming(resp, cfg, host, tSend, now)
	}
	return runStreaming(resp, cfg, host, tSend, now)
}

func runStreaming(resp *http.Response, cfg Config, host string, tSend time.Time, now func() time.Time) (Result, error) {
	res := Result{Model: cfg.Model, Host: host, PromptTokens: -1, Streamed: true}

	var tFirst, tLast, tAnswer time.Time
	textRunes, reasoningRunes := 0, 0
	var u *usage

	sc := sse.NewScanner(resp.Body)
	for sc.Scan() {
		var chunk streamChunk
		if err := json.Unmarshal([]byte(sc.Data()), &chunk); err != nil {
			if cfg.Warnf != nil {
				cfg.Warnf("skipping malformed SSE chunk: %v", err)
			}
			continue // skip malformed JSON
		}
		if chunk.Usage != nil {
			u = chunk.Usage
		}
		answer, thinking := 0, 0
		if len(chunk.Choices) > 0 {
			delta := chunk.Choices[0].Delta
			answer = utf8.RuneCountInString(delta.Content)
			// Servers migrating between the two names may send both with the
			// same text; count it once.
			thinking = utf8.RuneCountInString(string(cmp.Or(delta.ReasoningContent, delta.Reasoning)))
		}
		if answer+thinking > 0 {
			t := now()
			if tFirst.IsZero() {
				tFirst = t
			} else {
				res.ITL = append(res.ITL, t.Sub(tLast))
			}
			if answer > 0 && tAnswer.IsZero() {
				tAnswer = t
			}
			tLast = t
			textRunes += answer + thinking
			reasoningRunes += thinking
		}
	}
	if err := sc.Err(); err != nil {
		return Result{}, err
	}
	tEnd := now()

	if u != nil {
		res.PromptTokens = u.PromptTokens
		res.OutputTokens = u.outputTokens()
		res.TokensExact = true
	} else {
		res.OutputTokens = estimateTokens(textRunes, cfg.CharsPerToken)
		res.TokensExact = false
	}

	// Split the output into thinking and answer tokens. Prefer the server's
	// own count; otherwise apportion the total by streamed text length.
	reported, ok := u.reasoningTokens()
	switch {
	case ok && reported > 0:
		res.ReasoningTokens = min(reported, res.OutputTokens)
		res.ReasoningExact = true
		// Thinking the server counted but never streamed (OpenAI o-series,
		// gpt-5) happened before the first visible token, so it must not be
		// divided by the visible generation window.
		res.HiddenReasoning = reasoningRunes == 0
	case reasoningRunes > 0 && textRunes > 0:
		res.ReasoningTokens = int(math.Round(float64(res.OutputTokens) * float64(reasoningRunes) / float64(textRunes)))
	}
	res.Reasoning = res.ReasoningTokens > 0 || reasoningRunes > 0

	if !tFirst.IsZero() {
		res.TTFT = tFirst.Sub(tSend)
		res.GenTime = tLast.Sub(tFirst)
		res.TotalWall = tLast.Sub(tSend)
	} else {
		res.TotalWall = tEnd.Sub(tSend)
	}
	if !tAnswer.IsZero() {
		res.TTFA = tAnswer.Sub(tSend)
	}
	res.Cost = requestCost(cfg, res.PromptTokens, res.OutputTokens)
	return res, nil
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
		res.OutputTokens = cr.Usage.outputTokens()
		res.TokensExact = true
		if n, ok := cr.Usage.reasoningTokens(); ok && n > 0 {
			res.ReasoningTokens = min(n, res.OutputTokens)
			res.ReasoningExact = true
			res.Reasoning = true
		}
	} else {
		content := ""
		if len(cr.Choices) > 0 {
			content = cr.Choices[0].Message.Content
		}
		res.OutputTokens = estimateTokens(utf8.RuneCountInString(content), cfg.CharsPerToken)
		res.TokensExact = false
	}
	res.Cost = requestCost(cfg, res.PromptTokens, res.OutputTokens)
	return res, nil
}
