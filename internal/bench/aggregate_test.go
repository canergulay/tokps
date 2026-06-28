package bench

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPercentileInterpolates(t *testing.T) {
	vals := []float64{50, 10, 30, 20, 40} // unsorted on purpose
	cases := []struct {
		p    float64
		want float64
	}{
		{0.0, 10},
		{0.5, 30},
		{0.9, 46}, // between 40 and 50, 60% of the way
		{1.0, 50},
	}
	for _, c := range cases {
		if got := percentile(vals, c.p); got != c.want {
			t.Errorf("percentile(%.2f) = %v, want %v", c.p, got, c.want)
		}
	}
}

func TestPercentileEdgeCases(t *testing.T) {
	if got := percentile(nil, 0.5); got != 0 {
		t.Errorf("percentile(nil) = %v, want 0", got)
	}
	if got := percentile([]float64{42}, 0.9); got != 42 {
		t.Errorf("percentile(single) = %v, want 42", got)
	}
}

func TestSummaryStatsReportMedianAndRange(t *testing.T) {
	s := Summary{Results: []Result{
		{OutputTokens: 100, GenTime: 2 * time.Second, TotalWall: 4 * time.Second, TTFT: 1 * time.Second, PromptTokens: 12, TokensExact: true},
		{OutputTokens: 100, GenTime: 4 * time.Second, TotalWall: 8 * time.Second, TTFT: 3 * time.Second, PromptTokens: 12, TokensExact: true},
	}}
	// TPS values: 99/2=49.5 and 99/4=24.75. p50 is the midpoint; min/max the ends.
	tps := s.GenTPS()
	if tps.P50 != 37.125 || tps.Min != 24.75 || tps.Max != 49.5 {
		t.Errorf("GenTPS = %+v, want {Min:24.75 P50:37.125 Max:49.5}", tps)
	}
	// TTFT seconds: 1 and 3 -> p50 = 2, range 1..3.
	ttft := s.TTFT()
	if ttft.P50 != 2 || ttft.Min != 1 || ttft.Max != 3 {
		t.Errorf("TTFT = %+v, want {Min:1 P50:2 Max:3}", ttft)
	}
	if !s.Exact() {
		t.Errorf("Exact = false, want true")
	}
	if got := s.PromptTokens(); got != 12 {
		t.Errorf("PromptTokens = %d, want 12", got)
	}
}

func TestSummaryITLPoolsGapsAsP50P95Millis(t *testing.T) {
	s := Summary{Results: []Result{
		{ITL: []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}},
		{ITL: []time.Duration{30 * time.Millisecond, 40 * time.Millisecond}},
	}}
	p50, p95, ok := s.ITL()
	if !ok {
		t.Fatal("ok = false, want true")
	}
	// pooled ms = [10,20,30,40]; p50 = 25, p95 = 30 + 0.85*10 = 38.5
	if p50 != 25 {
		t.Errorf("p50 = %v ms, want 25", p50)
	}
	if p95 != 38.5 {
		t.Errorf("p95 = %v ms, want 38.5", p95)
	}
}

func TestSummaryITLNotOkWhenNoGaps(t *testing.T) {
	s := Summary{Results: []Result{{}}}
	if _, _, ok := s.ITL(); ok {
		t.Error("ok = true, want false (no streaming gaps)")
	}
}

func TestRunNConcurrencyFiresParallelStreams(t *testing.T) {
	var active, maxActive, calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		n := active.Add(1)
		for { // track the high-water mark of concurrent in-flight requests
			m := maxActive.Load()
			if n <= m || maxActive.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
		active.Add(-1)
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{}}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5}}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
		if fl != nil {
			fl.Flush()
		}
	}))
	defer ts.Close()

	sum, err := RunN(context.Background(), testConfig(ts.URL), 2, 1, 4)
	if err != nil {
		t.Fatalf("RunN error: %v", err)
	}
	if sum.Concurrency != 4 {
		t.Errorf("Concurrency = %d, want 4", sum.Concurrency)
	}
	if got := calls.Load(); got != 12 {
		t.Errorf("server calls = %d, want 12 (3 batches x 4 streams)", got)
	}
	if len(sum.Results) != 8 {
		t.Errorf("results = %d, want 8 (2 runs x 4 streams)", len(sum.Results))
	}
	if len(sum.BatchTPS) != 2 {
		t.Errorf("BatchTPS = %d, want 2 (one aggregate per run)", len(sum.BatchTPS))
	}
	if maxActive.Load() < 2 {
		t.Errorf("maxActive = %d, expected parallel overlap (>=2)", maxActive.Load())
	}
}

func TestParseLevels(t *testing.T) {
	got, err := ParseLevels("1,2, 4 ,8")
	if err != nil {
		t.Fatalf("ParseLevels error: %v", err)
	}
	want := []int{1, 2, 4, 8}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %d, want %d", i, got[i], want[i])
		}
	}
	if _, err := ParseLevels("1,x"); err == nil {
		t.Error("expected error on non-numeric level")
	}
	if _, err := ParseLevels("0"); err == nil {
		t.Error("expected error on level < 1")
	}
	if _, err := ParseLevels(""); err == nil {
		t.Error("expected error on empty input")
	}
}

func TestRunSweepRunsEachLevel(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{}}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5}}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
		if fl != nil {
			fl.Flush()
		}
	}))
	defer ts.Close()

	sums, err := RunSweep(context.Background(), testConfig(ts.URL), 1, 0, []int{1, 2})
	if err != nil {
		t.Fatalf("RunSweep error: %v", err)
	}
	if len(sums) != 2 {
		t.Fatalf("summaries = %d, want 2", len(sums))
	}
	if sums[0].Concurrency != 1 || sums[1].Concurrency != 2 {
		t.Errorf("concurrencies = %d,%d want 1,2", sums[0].Concurrency, sums[1].Concurrency)
	}
	// runs=1, warmup=0: level 1 -> 1 call, level 2 -> 2 calls = 3 total.
	if got := calls.Load(); got != 3 {
		t.Errorf("server calls = %d, want 3", got)
	}
}

func TestSummaryAggregateTPS(t *testing.T) {
	s := Summary{Concurrency: 4, BatchTPS: []float64{100, 200}}
	a := s.AggregateTPS()
	if a.Min != 100 || a.P50 != 150 || a.Max != 200 {
		t.Errorf("AggregateTPS = %+v, want {Min:100 P50:150 Max:200}", a)
	}
}

func TestRunNDiscardsWarmupKeepsMeasuredRuns(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{}}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5}}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
		if fl != nil {
			fl.Flush()
		}
	}))
	defer ts.Close()

	sum, err := RunN(context.Background(), testConfig(ts.URL), 2, 1, 1)
	if err != nil {
		t.Fatalf("RunN error: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("server calls = %d, want 3 (1 warmup + 2 measured)", got)
	}
	if len(sum.Results) != 2 {
		t.Errorf("kept results = %d, want 2", len(sum.Results))
	}
	if sum.Warmup != 1 {
		t.Errorf("Warmup = %d, want 1", sum.Warmup)
	}
	if sum.Model != "test-model" {
		t.Errorf("Model = %q, want test-model", sum.Model)
	}
}
