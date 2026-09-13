package bench

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func TestPercentileSortedInterpolates(t *testing.T) {
	sorted := []float64{10, 20, 30, 40, 50}
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
		if got := percentileSorted(sorted, c.p); got != c.want {
			t.Errorf("percentileSorted(%.2f) = %v, want %v", c.p, got, c.want)
		}
	}
}

func TestPercentileSortedEdgeCases(t *testing.T) {
	if got := percentileSorted(nil, 0.5); got != 0 {
		t.Errorf("percentileSorted(nil) = %v, want 0", got)
	}
	if got := percentileSorted([]float64{42}, 0.9); got != 42 {
		t.Errorf("percentileSorted(single) = %v, want 42", got)
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

func TestRunNEmitsProgressPerBatch(t *testing.T) {
	ts := sseServer(t, []string{
		`{"choices":[{"delta":{"content":"hi"}}]}`,
		`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":3,"completion_tokens":5}}`,
	})
	defer ts.Close()

	cfg := testConfig(ts.URL)
	var events []ProgressEvent
	cfg.Progress = func(ev ProgressEvent) { events = append(events, ev) }

	if _, err := RunN(context.Background(), cfg, 2, 1, 1); err != nil {
		t.Fatalf("RunN error: %v", err)
	}
	want := []struct {
		phase        string
		index, total int
	}{{"warmup", 1, 1}, {"run", 1, 2}, {"run", 2, 2}}
	if len(events) != len(want) {
		t.Fatalf("events = %+v, want %d events", events, len(want))
	}
	for i, w := range want {
		ev := events[i]
		if ev.Phase != w.phase || ev.Index != w.index || ev.Total != w.total || ev.Concurrency != 1 || ev.Label != "" {
			t.Errorf("events[%d] = %+v, want phase=%s %d/%d concurrency=1 label=\"\"", i, ev, w.phase, w.index, w.total)
		}
	}
	if events[0].BatchTPS != 0 {
		t.Errorf("warmup BatchTPS = %v, want 0", events[0].BatchTPS)
	}
	if events[1].BatchTPS <= 0 {
		t.Errorf("run BatchTPS = %v, want > 0", events[1].BatchTPS)
	}
}

func TestWithLabelStampsEvents(t *testing.T) {
	var got ProgressEvent
	cfg := withLabel(Config{Progress: func(ev ProgressEvent) { got = ev }}, "c=4")
	cfg.progress(ProgressEvent{Phase: "run", Index: 1, Total: 1})
	if got.Label != "c=4" || got.Phase != "run" {
		t.Errorf("forwarded event = %+v, want Label=c=4 Phase=run", got)
	}
	// withLabel on a Config without Progress stays a no-op.
	if withLabel(Config{}, "x").Progress != nil {
		t.Error("withLabel should not install a callback when none is configured")
	}
}

// scriptedServer streams a normal completion, except for requests whose
// 1-based arrival number makes fail(n) true — those get a 429.
func scriptedServer(t *testing.T, fail func(n int) bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1))
		if fail(n) {
			http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{}}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	return ts, &calls
}

func TestRunNRecordsFailedStreamAndContinues(t *testing.T) {
	// runs=3, warmup=0, concurrency=1: the 2nd measured request is 429'd.
	ts, _ := scriptedServer(t, func(n int) bool { return n == 2 })
	defer ts.Close()

	cfg := testConfig(ts.URL)
	var events []ProgressEvent
	cfg.Progress = func(ev ProgressEvent) { events = append(events, ev) }

	sum, err := RunN(context.Background(), cfg, 3, 0, 1)
	if err != nil {
		t.Fatalf("RunN error: %v, want nil (a partial failure is recorded, not fatal)", err)
	}
	if sum.Streams != 3 || sum.StreamCount() != 3 {
		t.Errorf("Streams = %d, want 3", sum.Streams)
	}
	if sum.Failed() != 1 {
		t.Fatalf("Failed = %d, want 1 (errors=%+v)", sum.Failed(), sum.Errors)
	}
	if len(sum.Results) != 2 {
		t.Errorf("Results = %d, want 2", len(sum.Results))
	}
	if len(sum.BatchTPS) != 3 {
		t.Errorf("BatchTPS = %d, want 3 (the failed batch still counts as a run)", len(sum.BatchTPS))
	}
	if sum.BatchTPS[1] != 0 {
		t.Errorf("BatchTPS[1] = %v, want 0 for the failed batch", sum.BatchTPS[1])
	}
	if e := sum.Errors[0]; e.Batch != 2 || e.Status != 429 || !strings.Contains(e.Err, "429") {
		t.Errorf("Errors[0] = %+v, want Batch=2 Status=429", e)
	}
	if got := sum.ErrorRate(); math.Abs(got-1.0/3) > 1e-9 {
		t.Errorf("ErrorRate = %v, want 1/3", got)
	}
	if sum.AllFailed() {
		t.Error("AllFailed = true, want false")
	}
	if len(events) != 3 || events[1].Failed != 1 || events[0].Failed != 0 {
		t.Errorf("progress events = %+v, want the 2nd run to report 1 failed", events)
	}
}

func TestRunNAllFailedReturnsSummaryAndError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"nope"}`, http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	sum, err := RunN(context.Background(), testConfig(ts.URL), 2, 0, 2)
	var af *AllFailedError
	if !errors.As(err, &af) {
		t.Fatalf("error = %T (%v), want *AllFailedError", err, err)
	}
	if sum.Streams != 4 || sum.Failed() != 4 || !sum.AllFailed() {
		t.Errorf("Streams=%d Failed=%d AllFailed=%v, want 4/4/true", sum.Streams, sum.Failed(), sum.AllFailed())
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error = %q, want it to mention 503", err.Error())
	}
}

func TestRunNWarmupFailureAbortsWithDetail(t *testing.T) {
	ts, calls := scriptedServer(t, func(n int) bool { return n == 1 })
	defer ts.Close()

	sum, err := RunN(context.Background(), testConfig(ts.URL), 2, 1, 1)
	if err == nil {
		t.Fatal("expected a warmup failure to abort")
	}
	var af *AllFailedError
	if errors.As(err, &af) {
		t.Errorf("warmup failure should not be AllFailedError: %v", err)
	}
	if !strings.Contains(err.Error(), "warmup batch 1") {
		t.Errorf("error = %q, want warmup batch context", err.Error())
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("server calls = %d, want 1 (abort right after warmup)", got)
	}
	// The returned Summary still explains what happened, for sweep/compare rows.
	if !sum.AllFailed() || sum.Streams != 0 || len(sum.Errors) != 1 || sum.Errors[0].Batch != 0 || sum.Errors[0].Status != 429 {
		t.Errorf("warmup-failed summary = %+v, want AllFailed, Streams=0, one Batch=0 429 error", sum)
	}
	if sum.Model != "test-model" || sum.Concurrency != 1 {
		t.Errorf("warmup-failed summary should carry Model/Concurrency: %+v", sum)
	}
}

func TestRunNConcurrentBatchAggregateExcludesFailedStreams(t *testing.T) {
	// 4 parallel streams, 1 measured batch; the 2nd and 4th arrivals are 429'd.
	ts, _ := scriptedServer(t, func(n int) bool { return n%2 == 0 })
	defer ts.Close()

	sum, err := RunN(context.Background(), testConfig(ts.URL), 1, 0, 4)
	if err != nil {
		t.Fatalf("RunN error: %v", err)
	}
	if sum.Failed() != 2 || len(sum.Results) != 2 {
		t.Errorf("Failed=%d Results=%d, want 2/2", sum.Failed(), len(sum.Results))
	}
	if sum.BatchTPS[0] <= 0 {
		t.Errorf("BatchTPS[0] = %v, want > 0 from the surviving streams", sum.BatchTPS[0])
	}
}

func TestSummaryErrorGroupsSortsByCountThenLabel(t *testing.T) {
	s := Summary{Streams: 10, Errors: []StreamError{
		{Batch: 1, Status: 500, Err: "endpoint returned 500 Internal Server Error: x"},
		{Batch: 1, Status: 429, Err: "endpoint returned 429 Too Many Requests: y"},
		{Batch: 2, Status: 429, Err: "endpoint returned 429 Too Many Requests: y"},
		{Batch: 3, Err: "Post \"http://x\": dial tcp: connection refused"},
	}}
	got := s.ErrorGroups()
	want := []ErrorGroup{
		{"429 Too Many Requests", 2},
		{"500 Internal Server Error", 1},
		{"Post \"http://x\": dial tcp: connection refused", 1},
	}
	if len(got) != len(want) {
		t.Fatalf("groups = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("groups[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	long := Summary{Errors: []StreamError{{Err: strings.Repeat("x", 80)}}}
	if l := long.ErrorGroups()[0].Label; len([]rune(l)) != 61 || !strings.HasSuffix(l, "…") {
		t.Errorf("long label = %q, want 60 runes + ellipsis", l)
	}
	if l := (Summary{Errors: []StreamError{{Status: 529}}}).ErrorGroups()[0].Label; l != "529" {
		t.Errorf("non-standard status label = %q, want bare \"529\"", l)
	}
}

func TestSummaryStreamCountFallsBackForHandBuiltSummaries(t *testing.T) {
	s := Summary{Results: []Result{{}, {}}, Errors: []StreamError{{}}}
	if got := s.StreamCount(); got != 3 {
		t.Errorf("StreamCount = %d, want 3 (results + errors)", got)
	}
	if got := (Summary{}).ErrorRate(); got != 0 {
		t.Errorf("ErrorRate of empty = %v, want 0", got)
	}
}

func TestRunSweepKeepsFailedLevelAsRow(t *testing.T) {
	// Level 1 (1 warmup + 1 run = 2 calls) succeeds; every request after
	// that — level 4's warmup — is 429'd, the usual hosted-API behavior
	// under load. The curve must keep both rows.
	ts, _ := scriptedServer(t, func(n int) bool { return n > 2 })
	defer ts.Close()

	sums, err := RunSweep(context.Background(), testConfig(ts.URL), 1, 1, []int{1, 4})
	if err != nil {
		t.Fatalf("RunSweep error: %v, want nil (a later level's failure is a row)", err)
	}
	if len(sums) != 2 {
		t.Fatalf("summaries = %d, want 2", len(sums))
	}
	if sums[0].Failed() != 0 || len(sums[0].Results) != 1 {
		t.Errorf("level 1: failed=%d results=%d, want 0/1", sums[0].Failed(), len(sums[0].Results))
	}
	if !sums[1].AllFailed() || sums[1].Concurrency != 4 {
		t.Errorf("level 4 should be a failed row with Concurrency=4: %+v", sums[1])
	}
	if g := sums[1].ErrorGroups(); len(g) != 1 || g[0].Label != "429 Too Many Requests" {
		t.Errorf("level 4 groups = %+v, want one 429 group", g)
	}
}

func TestRunSweepFirstLevelFailureAborts(t *testing.T) {
	ts, _ := scriptedServer(t, func(int) bool { return true })
	defer ts.Close()

	if _, err := RunSweep(context.Background(), testConfig(ts.URL), 1, 0, []int{1, 2}); err == nil {
		t.Fatal("expected the canary level's failure to abort the sweep")
	}
}

func TestRunSweepStampsProgressLabel(t *testing.T) {
	ts, _ := scriptedServer(t, func(int) bool { return false })
	defer ts.Close()

	cfg := testConfig(ts.URL)
	var labels []string
	cfg.Progress = func(ev ProgressEvent) { labels = append(labels, ev.Label) }
	if _, err := RunSweep(context.Background(), cfg, 1, 0, []int{1, 2}); err != nil {
		t.Fatalf("RunSweep error: %v", err)
	}
	if strings.Join(labels, ",") != "c=1,c=2" {
		t.Errorf("labels = %v, want [c=1 c=2]", labels)
	}
}

func TestRunSweepInterruptionAborts(t *testing.T) {
	ts, _ := scriptedServer(t, func(int) bool { return false })
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cfg := testConfig(ts.URL)
	// Cancel once the first level has finished, so level 2 sees a dead ctx.
	cfg.Progress = func(ev ProgressEvent) {
		if ev.Label == "c=1" {
			cancel()
		}
	}
	_, err := RunSweep(ctx, cfg, 1, 0, []int{1, 2})
	var ie *InterruptedError
	if !errors.As(err, &ie) {
		t.Fatalf("error = %v, want *InterruptedError", err)
	}
}

func TestNewStreamErrorCapsText(t *testing.T) {
	se := newStreamError(1, errors.New(strings.Repeat("x", 600)))
	if n := utf8.RuneCountInString(se.Err); n != maxErrRunes+1 || !strings.HasSuffix(se.Err, "…") {
		t.Errorf("Err runes = %d, want %d + ellipsis", n, maxErrRunes+1)
	}
	if se.Status != 0 {
		t.Errorf("Status = %d, want 0 for a non-HTTP error", se.Status)
	}
}
