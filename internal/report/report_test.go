package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/canergulay/tokps/internal/bench"
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

func TestFormatSummaryShowsPercentilesAndRunCounts(t *testing.T) {
	s := bench.Summary{
		Model: "glm-5.2", Host: "api.z.ai", Warmup: 1,
		Results: []bench.Result{
			{PromptTokens: 39, OutputTokens: 200, TokensExact: true, Streamed: true,
				TTFT: 2600 * time.Millisecond, GenTime: 2700 * time.Millisecond, TotalWall: 5300 * time.Millisecond},
			{PromptTokens: 39, OutputTokens: 210, TokensExact: true, Streamed: true,
				TTFT: 2800 * time.Millisecond, GenTime: 2900 * time.Millisecond, TotalWall: 5700 * time.Millisecond},
		},
	}
	var buf bytes.Buffer
	FormatSummary(&buf, s)
	out := buf.String()

	for _, want := range []string{"glm-5.2", "api.z.ai", "2 runs", "1 warmup", "p50", "range", "(generation, N-1)", "(exact"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatSummarySingleRunUsesDetailedView(t *testing.T) {
	s := bench.Summary{
		Model: "local", Host: "localhost:1234", Warmup: 0,
		Results: []bench.Result{
			{PromptTokens: 5, OutputTokens: 50, TokensExact: true, Streamed: true,
				TTFT: time.Second, GenTime: 2 * time.Second, TotalWall: 3 * time.Second},
		},
	}
	var buf bytes.Buffer
	FormatSummary(&buf, s)
	out := buf.String()

	// One measured run: fall back to the detailed single-shot block, no percentiles.
	if strings.Contains(out, "p50") {
		t.Errorf("single run should not show percentiles:\n%s", out)
	}
	if !strings.Contains(out, "time to first") {
		t.Errorf("single run should show the detailed block:\n%s", out)
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
