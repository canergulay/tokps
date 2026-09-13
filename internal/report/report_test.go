package report

import (
	"bytes"
	"encoding/json"
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
	FormatSummary(&buf, s, false)
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
	FormatSummary(&buf, s, false)
	out := buf.String()

	// One measured run: fall back to the detailed single-shot block, no percentiles.
	if strings.Contains(out, "p50") {
		t.Errorf("single run should not show percentiles:\n%s", out)
	}
	if !strings.Contains(out, "time to first") {
		t.Errorf("single run should show the detailed block:\n%s", out)
	}
}

func TestFormatSummaryConcurrentShowsAggregate(t *testing.T) {
	s := bench.Summary{
		Model: "glm-5.2", Host: "api.z.ai", Warmup: 1, Concurrency: 4,
		BatchTPS: []float64{235, 245},
		Results: []bench.Result{
			{OutputTokens: 200, TokensExact: true, Streamed: true, TTFT: 2600 * time.Millisecond, GenTime: 2700 * time.Millisecond, TotalWall: 5300 * time.Millisecond},
			{OutputTokens: 210, TokensExact: true, Streamed: true, TTFT: 2800 * time.Millisecond, GenTime: 2900 * time.Millisecond, TotalWall: 5700 * time.Millisecond},
		},
	}
	var buf bytes.Buffer
	FormatSummary(&buf, s, false)
	out := buf.String()
	for _, want := range []string{"concurrency 4", "aggregate", "all streams"} {
		if !strings.Contains(out, want) {
			t.Errorf("concurrent output missing %q:\n%s", want, out)
		}
	}

	// A non-concurrent summary must not show the aggregate/concurrency line.
	s.Concurrency = 1
	var buf2 bytes.Buffer
	FormatSummary(&buf2, s, false)
	if strings.Contains(buf2.String(), "aggregate") || strings.Contains(buf2.String(), "concurrency") {
		t.Errorf("non-concurrent output should not show aggregate/concurrency:\n%s", buf2.String())
	}
}

func TestFormatJSONIncludesConcurrencyAndAggregate(t *testing.T) {
	s := bench.Summary{
		Model: "m", Host: "h", Warmup: 1, Concurrency: 4, BatchTPS: []float64{235, 245},
		Results: []bench.Result{
			{OutputTokens: 200, TokensExact: true, Streamed: true, TTFT: time.Second, GenTime: 2 * time.Second, TotalWall: 3 * time.Second},
		},
	}
	var buf bytes.Buffer
	if err := FormatJSON(&buf, s); err != nil {
		t.Fatalf("FormatJSON error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["concurrency"].(float64) != 4 {
		t.Errorf("concurrency = %v, want 4", m["concurrency"])
	}
	if agg, ok := m["aggregate_tps"].(map[string]any); !ok || agg["p50"] == nil {
		t.Errorf("missing aggregate_tps.p50: %v", m["aggregate_tps"])
	}
}

func TestFormatSweepShowsCurve(t *testing.T) {
	sums := []bench.Summary{
		{Model: "glm-5.2", Host: "api.z.ai", Warmup: 1, Concurrency: 1, BatchTPS: []float64{73},
			Results: []bench.Result{{OutputTokens: 200, Streamed: true, TokensExact: true, TTFT: 2600 * time.Millisecond, GenTime: 2700 * time.Millisecond, TotalWall: 5300 * time.Millisecond}}},
		{Model: "glm-5.2", Host: "api.z.ai", Warmup: 1, Concurrency: 4, BatchTPS: []float64{240},
			Results: []bench.Result{{OutputTokens: 200, Streamed: true, TokensExact: true, TTFT: 3100 * time.Millisecond, GenTime: 2700 * time.Millisecond, TotalWall: 5300 * time.Millisecond}}},
	}
	var buf bytes.Buffer
	FormatSweep(&buf, sums)
	out := buf.String()
	for _, want := range []string{"sweep", "concurrency", "aggregate", "glm-5.2"} {
		if !strings.Contains(out, want) {
			t.Errorf("sweep output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "–") {
		t.Errorf("sweep rows should show min–max ranges:\n%s", out)
	}
}

func TestFormatSweepJSONIsArray(t *testing.T) {
	sums := []bench.Summary{
		{Concurrency: 1, BatchTPS: []float64{73}, Results: []bench.Result{{OutputTokens: 200, Streamed: true, TokensExact: true, TTFT: time.Second, GenTime: 2 * time.Second, TotalWall: 3 * time.Second}}},
		{Concurrency: 4, BatchTPS: []float64{240}, Results: []bench.Result{{OutputTokens: 200, Streamed: true, TokensExact: true, TTFT: time.Second, GenTime: 2 * time.Second, TotalWall: 3 * time.Second}}},
	}
	var buf bytes.Buffer
	if err := FormatSweepJSON(&buf, sums); err != nil {
		t.Fatalf("FormatSweepJSON error: %v", err)
	}
	var arr []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &arr); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(arr) != 2 {
		t.Fatalf("array length = %d, want 2", len(arr))
	}
	if arr[0]["concurrency"].(float64) != 1 {
		t.Errorf("arr[0].concurrency = %v, want 1", arr[0]["concurrency"])
	}
	if arr[1]["aggregate_tps"] == nil {
		t.Errorf("arr[1] missing aggregate_tps")
	}
}

func TestFormatSummaryDetailAddsInterTokenLatency(t *testing.T) {
	s := bench.Summary{
		Model: "m", Host: "h", Warmup: 1,
		Results: []bench.Result{
			{OutputTokens: 100, TokensExact: true, Streamed: true, TTFT: time.Second, GenTime: 2 * time.Second, TotalWall: 3 * time.Second, ITL: []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}},
			{OutputTokens: 100, TokensExact: true, Streamed: true, TTFT: time.Second, GenTime: 2 * time.Second, TotalWall: 3 * time.Second, ITL: []time.Duration{30 * time.Millisecond, 40 * time.Millisecond}},
		},
	}
	var on, off bytes.Buffer
	FormatSummary(&on, s, true)
	FormatSummary(&off, s, false)

	if !strings.Contains(on.String(), "ITL") || !strings.Contains(on.String(), "p95") {
		t.Errorf("detail output missing ITL/p95:\n%s", on.String())
	}
	if strings.Contains(off.String(), "ITL") {
		t.Errorf("non-detail output should not show ITL:\n%s", off.String())
	}
}

func TestFormatJSONEmitsParseableMetrics(t *testing.T) {
	ms := time.Millisecond
	s := bench.Summary{
		Model: "glm-5.2", Host: "api.z.ai", Warmup: 1,
		Results: []bench.Result{
			{PromptTokens: 39, OutputTokens: 200, TokensExact: true, Streamed: true, TTFT: 2600 * ms, GenTime: 2700 * ms, TotalWall: 5300 * ms, ITL: []time.Duration{10 * ms, 20 * ms}},
			{PromptTokens: 39, OutputTokens: 210, TokensExact: true, Streamed: true, TTFT: 2800 * ms, GenTime: 2900 * ms, TotalWall: 5700 * ms, ITL: []time.Duration{30 * ms, 40 * ms}},
		},
	}
	var buf bytes.Buffer
	if err := FormatJSON(&buf, s); err != nil {
		t.Fatalf("FormatJSON error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if m["model"] != "glm-5.2" {
		t.Errorf("model = %v, want glm-5.2", m["model"])
	}
	if m["runs"].(float64) != 2 {
		t.Errorf("runs = %v, want 2", m["runs"])
	}
	if tps, ok := m["tps"].(map[string]any); !ok || tps["p50"] == nil {
		t.Errorf("missing tps.p50: %v", m["tps"])
	}
	if itl, ok := m["itl_ms"].(map[string]any); !ok || itl["p95"] == nil {
		t.Errorf("missing itl_ms.p95: %v", m["itl_ms"])
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

func TestFormatShowsCostWhenPresent(t *testing.T) {
	r := bench.Result{
		Model: "m", Host: "h", PromptTokens: 1000, OutputTokens: 2000, TokensExact: true,
		TTFT: time.Second, GenTime: 2 * time.Second, TotalWall: 3 * time.Second, Streamed: true,
		Cost: 0.03,
	}
	var buf bytes.Buffer
	Format(&buf, r)
	out := buf.String()
	if !strings.Contains(out, "cost") || !strings.Contains(out, "$") {
		t.Errorf("cost line missing:\n%s", out)
	}

	// Without a configured cost, no cost line appears.
	r.Cost = 0
	buf.Reset()
	Format(&buf, r)
	if strings.Contains(buf.String(), "cost") {
		t.Errorf("cost line should be omitted when cost is 0:\n%s", buf.String())
	}
}

func TestFormatSummaryShowsMedianCost(t *testing.T) {
	s := bench.Summary{
		Model: "m", Host: "h", Warmup: 1, CostIn: 6, CostOut: 12,
		Results: []bench.Result{
			{OutputTokens: 200, TokensExact: true, Streamed: true, TTFT: time.Second, GenTime: 2 * time.Second, TotalWall: 3 * time.Second, Cost: 0.02},
			{OutputTokens: 200, TokensExact: true, Streamed: true, TTFT: time.Second, GenTime: 2 * time.Second, TotalWall: 3 * time.Second, Cost: 0.04},
		},
	}
	var buf bytes.Buffer
	FormatSummary(&buf, s, false)
	out := buf.String()
	if !strings.Contains(out, "cost") || !strings.Contains(out, "median") {
		t.Errorf("median cost line missing:\n%s", out)
	}
}

func TestFormatJSONIncludesCostWhenConfigured(t *testing.T) {
	s := bench.Summary{
		Model: "m", Host: "h", Warmup: 1, CostIn: 6, CostOut: 12,
		Results: []bench.Result{
			{OutputTokens: 200, TokensExact: true, Streamed: true, TTFT: time.Second, GenTime: 2 * time.Second, TotalWall: 3 * time.Second, Cost: 0.03},
		},
	}
	var buf bytes.Buffer
	if err := FormatJSON(&buf, s); err != nil {
		t.Fatalf("FormatJSON error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if c, ok := m["cost_usd"].(map[string]any); !ok || c["p50"] == nil {
		t.Errorf("missing cost_usd.p50: %v", m["cost_usd"])
	}
	if m["cost_in_per_1m"].(float64) != 6 || m["cost_out_per_1m"].(float64) != 12 {
		t.Errorf("cost rates not echoed: %v", m)
	}
}

func okResult(ttft, gen, wall time.Duration) bench.Result {
	return bench.Result{OutputTokens: 100, TokensExact: true, Streamed: true, TTFT: ttft, GenTime: gen, TotalWall: wall}
}

func repeatErr(n int, e bench.StreamError) []bench.StreamError {
	out := make([]bench.StreamError, n)
	for i := range out {
		out[i] = e
	}
	return out
}

func TestFormatSummaryShowsErrorsLine(t *testing.T) {
	s := bench.Summary{
		Model: "m", Host: "h", Streams: 4, BatchTPS: []float64{50, 0, 50, 50},
		Results: []bench.Result{
			okResult(time.Second, 2*time.Second, 3*time.Second),
			okResult(time.Second, 2*time.Second, 3*time.Second),
			okResult(time.Second, 2*time.Second, 3*time.Second),
		},
		Errors: []bench.StreamError{{Batch: 2, Status: 429, Err: "endpoint returned 429 Too Many Requests: slow down"}},
	}
	var buf bytes.Buffer
	FormatSummary(&buf, s, false)
	out := buf.String()
	if !strings.Contains(out, "errors            1/4 streams   (429 Too Many Requests ×1)") {
		t.Errorf("output missing the errors line:\n%s", out)
	}
	if !strings.Contains(out, "4 runs") {
		t.Errorf("run count should count the failed batch:\n%s", out)
	}

	s.Errors = nil
	buf.Reset()
	FormatSummary(&buf, s, false)
	if strings.Contains(buf.String(), "errors") {
		t.Errorf("no errors line when nothing failed:\n%s", buf.String())
	}
}

func TestFormatSummaryOneSurvivorWithFailuresShowsPercentileBlock(t *testing.T) {
	s := bench.Summary{
		Model: "m", Host: "h", Streams: 3, BatchTPS: []float64{0, 50, 0},
		Results: []bench.Result{okResult(time.Second, 2*time.Second, 3*time.Second)},
		Errors:  []bench.StreamError{{Batch: 1, Status: 429}, {Batch: 3, Status: 429}},
	}
	var buf bytes.Buffer
	FormatSummary(&buf, s, false)
	out := buf.String()
	if !strings.Contains(out, "2/3 streams") {
		t.Errorf("expected an errors line for 2/3 failures:\n%s", out)
	}
	if strings.Contains(out, "time to first") {
		t.Errorf("must not fall back to the single-shot block when streams failed:\n%s", out)
	}
}

func TestFormatSummaryNothingToShowWhenAllFailed(t *testing.T) {
	var buf bytes.Buffer
	FormatSummary(&buf, bench.Summary{Streams: 2, Errors: repeatErr(2, bench.StreamError{Status: 500})}, false)
	if buf.Len() != 0 {
		t.Errorf("all-failed summary should render nothing (main reports the error):\n%s", buf.String())
	}
}

func TestFormatJSONIncludesErrors(t *testing.T) {
	s := bench.Summary{
		Model: "m", Host: "h", Streams: 2, BatchTPS: []float64{50, 0},
		Results: []bench.Result{okResult(time.Second, 2*time.Second, 3*time.Second)},
		Errors:  []bench.StreamError{{Batch: 2, Status: 429, Err: "endpoint returned 429 Too Many Requests: x"}},
	}
	var buf bytes.Buffer
	if err := FormatJSON(&buf, s); err != nil {
		t.Fatalf("FormatJSON error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["streams"].(float64) != 2 || m["errors"].(float64) != 1 || m["error_rate"].(float64) != 0.5 {
		t.Errorf("streams/errors/error_rate = %v/%v/%v, want 2/1/0.5", m["streams"], m["errors"], m["error_rate"])
	}
	detail, ok := m["errors_detail"].([]any)
	if !ok || len(detail) != 1 {
		t.Fatalf("errors_detail = %v, want one entry", m["errors_detail"])
	}
	e := detail[0].(map[string]any)
	if e["batch"].(float64) != 2 || e["status"].(float64) != 429 || e["error"] == "" {
		t.Errorf("errors_detail[0] = %v, want batch=2 status=429 error=<text>", e)
	}

	// No failures: counts are zero and errors_detail is omitted.
	s.Errors = nil
	buf.Reset()
	_ = FormatJSON(&buf, s)
	var clean map[string]any
	if err := json.Unmarshal(buf.Bytes(), &clean); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if clean["errors"].(float64) != 0 {
		t.Errorf("clean run errors = %v, want 0", clean["errors"])
	}
	if _, present := clean["errors_detail"]; present {
		t.Errorf("clean run JSON should omit errors_detail, got %v", clean["errors_detail"])
	}
}

func TestFormatSweepRendersFailedLevelAndErrorsColumn(t *testing.T) {
	sums := []bench.Summary{
		{Model: "m", Host: "h", Concurrency: 1, Streams: 1, BatchTPS: []float64{73},
			Results: []bench.Result{okResult(time.Second, 2*time.Second, 3*time.Second)}},
		{Model: "m", Host: "h", Concurrency: 4, Streams: 4, BatchTPS: []float64{150},
			Results: []bench.Result{okResult(time.Second, 2*time.Second, 3*time.Second), okResult(time.Second, 2*time.Second, 3*time.Second)},
			Errors:  repeatErr(2, bench.StreamError{Batch: 1, Status: 429, Err: "x"})},
		{Model: "m", Host: "h", Concurrency: 8, Streams: 8, BatchTPS: []float64{0},
			Errors: repeatErr(8, bench.StreamError{Batch: 1, Status: 429, Err: "x"})},
	}
	var buf bytes.Buffer
	FormatSweep(&buf, sums)
	out := buf.String()
	for _, want := range []string{"errors", "failed (429 Too Many Requests ×8)", "2/4"} {
		if !strings.Contains(out, want) {
			t.Errorf("sweep output missing %q:\n%s", want, out)
		}
	}

	buf.Reset()
	if err := FormatSweepJSON(&buf, sums); err != nil {
		t.Fatalf("FormatSweepJSON error: %v", err)
	}
	var arr []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &arr); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if arr[2]["failed"] != true || arr[2]["errors"].(float64) != 8 || arr[2]["error_rate"].(float64) != 1 {
		t.Errorf("failed level JSON = %v, want failed=true errors=8 error_rate=1", arr[2])
	}
	if arr[0]["failed"] != false || arr[0]["errors"].(float64) != 0 || arr[0]["error_rate"].(float64) != 0 {
		t.Errorf("ok level JSON = %v, want failed=false errors=0", arr[0])
	}
	if arr[1]["error_rate"].(float64) != 0.5 {
		t.Errorf("partial level error_rate = %v, want 0.5", arr[1]["error_rate"])
	}
}
