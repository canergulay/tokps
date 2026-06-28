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
