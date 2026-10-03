package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/canergulay/tokps/internal/bench"
)

func thinkingResult(ttfa time.Duration) bench.Result {
	r := okResult(500*time.Millisecond, 2*time.Second, 2500*time.Millisecond)
	r.Reasoning, r.ReasoningExact, r.ReasoningTokens, r.TTFA = true, true, 80, ttfa
	return r
}

func TestFormatSummaryShowsThinkingAndAnswer(t *testing.T) {
	s := bench.Summary{Model: "deepseek-flash", Host: "api.deepseek.com", BatchTPS: []float64{1, 1},
		Results: []bench.Result{thinkingResult(1500 * time.Millisecond), thinkingResult(1700 * time.Millisecond)}}
	var buf bytes.Buffer
	FormatSummary(&buf, s, false)
	out := buf.String()
	for _, want := range []string{
		"thinking          80   (exact, median; answer 20)",
		"answer   p50 1.60s   range 1.50s–1.70s   (first answer token, after thinking)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatSummaryAnswerNotReached(t *testing.T) {
	s := bench.Summary{Model: "m", Host: "h", BatchTPS: []float64{1, 1},
		Results: []bench.Result{thinkingResult(0), thinkingResult(0)}}
	var buf bytes.Buffer
	FormatSummary(&buf, s, false)
	if !strings.Contains(buf.String(), "answer   not reached   (thinking used the whole budget — raise --max-tokens)") {
		t.Errorf("want a not-reached hint:\n%s", buf.String())
	}

	s.Results[0].TTFA = time.Second
	buf.Reset()
	FormatSummary(&buf, s, false)
	if !strings.Contains(buf.String(), "1/2 runs reached it") {
		t.Errorf("want a partial-reach note:\n%s", buf.String())
	}
}

func TestFormatHiddenReasoning(t *testing.T) {
	r := okResult(3*time.Second, time.Second, 4*time.Second)
	r.Reasoning, r.ReasoningExact, r.HiddenReasoning, r.ReasoningTokens = true, true, true, 90
	var buf bytes.Buffer
	Format(&buf, r)
	out := buf.String()
	if !strings.Contains(out, "thinking          90   (exact, hidden — not streamed, excluded from TPS)") {
		t.Errorf("want the hidden-thinking line:\n%s", out)
	}
	if strings.Contains(out, "first answer") {
		t.Errorf("hidden thinking has no separate answer time:\n%s", out)
	}
}

func TestFormatPlainModelHasNoThinkingLines(t *testing.T) {
	s := bench.Summary{Model: "m", Host: "h", BatchTPS: []float64{1, 1},
		Results: []bench.Result{okResult(time.Second, time.Second, 2*time.Second), okResult(time.Second, time.Second, 2*time.Second)}}
	var buf bytes.Buffer
	FormatSummary(&buf, s, false)
	FormatMarkdown(&buf, s, false)
	if out := buf.String(); strings.Contains(out, "thinking") || strings.Contains(out, "answer") {
		t.Errorf("plain model output mentions thinking:\n%s", out)
	}
}

func TestFormatJSONIncludesReasoning(t *testing.T) {
	s := bench.Summary{Model: "m", Host: "h", BatchTPS: []float64{1},
		Results: []bench.Result{thinkingResult(1500 * time.Millisecond)}}
	var buf bytes.Buffer
	if err := FormatJSON(&buf, s); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	r, ok := m["reasoning"].(map[string]any)
	if !ok || r["tokens_median"].(float64) != 80 || r["answer_runs"].(float64) != 1 || r["ttfa_s"].(map[string]any)["p50"].(float64) != 1.5 {
		t.Errorf("reasoning = %v", m["reasoning"])
	}
	run := m["runs_detail"].([]any)[0].(map[string]any)
	if run["reasoning_tokens"].(float64) != 80 || run["ttfa_s"].(float64) != 1.5 {
		t.Errorf("runs_detail = %v", run)
	}
}

func TestFormatJSONPlainRunsOmitReasoningFields(t *testing.T) {
	s := bench.Summary{Model: "m", Host: "h", BatchTPS: []float64{1},
		Results: []bench.Result{okResult(time.Second, time.Second, 2*time.Second)}}
	s.Results[0].TTFA = time.Second
	var buf bytes.Buffer
	if err := FormatJSON(&buf, s); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); strings.Contains(out, "reasoning") || strings.Contains(out, "ttfa_s") {
		t.Errorf("plain-model JSON carries reasoning fields:\n%s", out)
	}
}

func TestFormatCompareMixedHostsAndAnswerColumn(t *testing.T) {
	sums := []bench.Summary{
		{Model: "deepseek-flash", Host: "api.deepseek.com", BatchTPS: []float64{1}, Results: []bench.Result{thinkingResult(1500 * time.Millisecond)}},
		{Model: "gpt-4o-mini", Host: "api.openai.com", BatchTPS: []float64{1}, Results: []bench.Result{okResult(time.Second, time.Second, 2*time.Second)}},
	}
	var buf bytes.Buffer
	FormatCompare(&buf, sums)
	FormatCompareMarkdown(&buf, sums)
	out := buf.String()
	for _, want := range []string{"tokps — compare  (1 runs", "target", "answer p50", "deepseek-flash @ api.deepseek.com", "gpt-4o-mini @ api.openai.com", "1.50s",
		"| target | TTFT p50 | answer p50 | TPS p50 (range) | e2e p50 | errors |", "|---|---|---|---|---|---|"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "compare @") {
		t.Errorf("mixed-host header should not name one host:\n%s", out)
	}
}

func TestFormatCompareNonStreamedReasoningHasNoAnswerColumn(t *testing.T) {
	r := okResult(0, 0, 2*time.Second)
	r.Streamed, r.Reasoning, r.ReasoningExact, r.ReasoningTokens = false, true, true, 80
	sums := []bench.Summary{
		{Model: "a", Host: "h", BatchTPS: []float64{1}, Results: []bench.Result{r}},
		{Model: "b", Host: "h", BatchTPS: []float64{1}, Results: []bench.Result{okResult(time.Second, time.Second, 2*time.Second)}},
	}
	var buf bytes.Buffer
	FormatCompare(&buf, sums)
	FormatCompareMarkdown(&buf, sums)
	if out := buf.String(); strings.Contains(out, "not reached") || strings.Contains(out, "answer p50") {
		t.Errorf("a non-streamed reasoning model cannot have an answer time:\n%s", out)
	}
}
