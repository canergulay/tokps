package bench

import (
	"context"
	"math"
	"testing"
	"time"
)

// DeepSeek, GLM and friends stream thinking in reasoning_content and report
// its size in usage.completion_tokens_details.reasoning_tokens.
func TestRunStreamingSplitsReasoningFromUsage(t *testing.T) {
	ts := sseServer(t, []string{
		`{"choices":[{"delta":{"reasoning_content":"think"}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"more"}}]}`,
		`{"choices":[{"delta":{"content":"391"}}]}`,
		`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":9,"completion_tokens":40,"completion_tokens_details":{"reasoning_tokens":30}}}`,
	})
	defer ts.Close()

	cfg := testConfig(ts.URL)
	cfg.Now = fakeClock(time.Second) // send=0s, chunks at 1s, 2s, 3s

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !res.Reasoning || !res.ReasoningExact || res.HiddenReasoning {
		t.Errorf("Reasoning/Exact/Hidden = %v/%v/%v, want true/true/false", res.Reasoning, res.ReasoningExact, res.HiddenReasoning)
	}
	if res.ReasoningTokens != 30 || res.AnswerTokens() != 10 {
		t.Errorf("thinking/answer = %d/%d, want 30/10", res.ReasoningTokens, res.AnswerTokens())
	}
	if res.TTFT != time.Second || res.TTFA != 3*time.Second {
		t.Errorf("TTFT/TTFA = %v/%v, want 1s/3s", res.TTFT, res.TTFA)
	}
	// Streamed thinking counts toward the generation rate: (40-1)/2s.
	if got := res.TPS(); math.Abs(got-19.5) > 1e-9 {
		t.Errorf("TPS = %v, want 19.5", got)
	}
}

func TestRunStreamingApportionsReasoningWithoutDetails(t *testing.T) {
	ts := sseServer(t, []string{
		`{"choices":[{"delta":{"reasoning_content":"aaaaaaaaaaaaaaa"}}]}`, // 15 runes
		`{"choices":[{"delta":{"content":"bbbbb"}}]}`,                     // 5 runes
		`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":9,"completion_tokens":100}}`,
	})
	defer ts.Close()

	res, err := Run(context.Background(), testConfig(ts.URL))
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !res.Reasoning || res.ReasoningExact || res.ReasoningTokens != 75 {
		t.Errorf("Reasoning=%v Exact=%v tokens=%d, want true/false/75 (15/20 of 100)", res.Reasoning, res.ReasoningExact, res.ReasoningTokens)
	}
}

// OpenAI's o-series and gpt-5 bill reasoning tokens they never stream. Those
// were generated before the first visible token, so dividing them by the
// visible window would inflate TPS several-fold.
func TestRunStreamingHiddenReasoningExcludedFromTPS(t *testing.T) {
	ts := sseServer(t, []string{
		`{"choices":[{"delta":{"content":"a"}}]}`,
		`{"choices":[{"delta":{"content":"b"}}]}`,
		`{"choices":[{"delta":{"content":"c"}}]}`,
		`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":9,"completion_tokens":100,"completion_tokens_details":{"reasoning_tokens":90}}}`,
	})
	defer ts.Close()

	cfg := testConfig(ts.URL)
	cfg.Now = fakeClock(time.Second)

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !res.HiddenReasoning || res.ReasoningTokens != 90 {
		t.Errorf("Hidden=%v tokens=%d, want true/90", res.HiddenReasoning, res.ReasoningTokens)
	}
	// 10 visible tokens over a 2s window: (10-1)/2 = 4.5, not (100-1)/2.
	if got := res.TPS(); math.Abs(got-4.5) > 1e-9 {
		t.Errorf("TPS = %v, want 4.5", got)
	}
}

func TestRunStreamingAnswerNotReached(t *testing.T) {
	ts := sseServer(t, []string{
		`{"choices":[{"delta":{"reasoning_content":"thinking"}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"still"}}]}`,
		`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":9,"completion_tokens":64,"completion_tokens_details":{"reasoning_tokens":64}}}`,
	})
	defer ts.Close()

	res, err := Run(context.Background(), testConfig(ts.URL))
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.TTFA != 0 || res.AnswerTokens() != 0 || res.HiddenReasoning {
		t.Errorf("TTFA=%v answer=%d hidden=%v, want 0/0/false", res.TTFA, res.AnswerTokens(), res.HiddenReasoning)
	}
}

func TestRunStreamingPlainModelHasNoReasoning(t *testing.T) {
	ts := sseServer(t, []string{
		`{"choices":[{"delta":{"content":"hi"}}]}`,
		`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":9,"completion_tokens":5,"completion_tokens_details":{"reasoning_tokens":0}}}`,
	})
	defer ts.Close()

	res, err := Run(context.Background(), testConfig(ts.URL))
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Reasoning || res.ReasoningTokens != 0 || res.HiddenReasoning {
		t.Errorf("plain model flagged as reasoning: %+v", res)
	}
	if res.TTFA != res.TTFT {
		t.Errorf("TTFA = %v, want it equal to TTFT (%v) without thinking", res.TTFA, res.TTFT)
	}
}

func TestSummaryTTFACountsRunsThatReachedTheAnswer(t *testing.T) {
	s := Summary{Results: []Result{
		{Reasoning: true, TTFA: 2 * time.Second},
		{Reasoning: true},
		{Reasoning: true, TTFA: 4 * time.Second},
	}}
	st, reached := s.TTFA()
	if reached != 2 || st.P50 != 3 || st.Min != 2 || st.Max != 4 {
		t.Errorf("TTFA = %+v reached=%d, want p50 3 (2–4) over 2 runs", st, reached)
	}
}

// OpenRouter, Ollama and newer vLLM stream thinking in delta.reasoning.
func TestRunStreamingReadsReasoningField(t *testing.T) {
	ts := sseServer(t, []string{
		`{"choices":[{"delta":{"reasoning":"hmm"}}]}`,
		`{"choices":[{"delta":{"reasoning":{"unexpected":"object"},"content":"ok"}}]}`,
		`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":9,"completion_tokens":20,"completion_tokens_details":{"reasoning_tokens":12}}}`,
	})
	defer ts.Close()

	cfg := testConfig(ts.URL)
	cfg.Now = fakeClock(time.Second)
	var warnings int
	cfg.Warnf = func(string, ...any) { warnings++ }

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if warnings != 0 {
		t.Errorf("a non-string reasoning field produced %d malformed-chunk warnings, want 0", warnings)
	}
	if res.HiddenReasoning || res.ReasoningTokens != 12 || res.TTFT != time.Second || res.TTFA != 2*time.Second {
		t.Errorf("hidden=%v thinking=%d TTFT=%v TTFA=%v, want streamed thinking 12, 1s, 2s", res.HiddenReasoning, res.ReasoningTokens, res.TTFT, res.TTFA)
	}
}
