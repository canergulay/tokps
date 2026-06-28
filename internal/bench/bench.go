package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/canergulay/tokps/internal/sse"
)

// avgCharsPerToken is the rule-of-thumb ratio used to estimate token counts
// when the server omits a usage block. ~4 chars/token is OpenAI's documented
// heuristic for English text — model-agnostic and independent of how the
// server chose to chunk the stream, unlike counting SSE events.
const avgCharsPerToken = 4

// estimateTokens approximates a token count from a rune count.
func estimateTokens(runes int) int {
	return int(math.Round(float64(runes) / avgCharsPerToken))
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
			// ReasoningContent carries thinking-mode tokens for reasoning
			// models (e.g. GLM-5.2, DeepSeek-R1). These are generated
			// tokens and count toward throughput.
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *usage `json:"usage"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
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
	textRunes := 0
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
		runes := 0
		if len(chunk.Choices) > 0 {
			delta := chunk.Choices[0].Delta
			runes = utf8.RuneCountInString(delta.Content) + utf8.RuneCountInString(delta.ReasoningContent)
		}
		if runes > 0 {
			t := now()
			if tFirst.IsZero() {
				tFirst = t
			} else {
				res.ITL = append(res.ITL, t.Sub(tLast))
			}
			tLast = t
			textRunes += runes
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
		res.OutputTokens = estimateTokens(textRunes)
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
		res.OutputTokens = estimateTokens(utf8.RuneCountInString(content))
		res.TokensExact = false
	}
	return res, nil
}
