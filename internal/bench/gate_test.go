package bench

import (
	"testing"
	"time"
)

func TestGateCheck(t *testing.T) {
	// TPS = (101-1)/2s = 50 tok/s, TTFT 0.5s.
	ok := Summary{Results: []Result{
		{OutputTokens: 101, GenTime: 2 * time.Second, TTFT: 500 * time.Millisecond, TotalWall: 3 * time.Second, Streamed: true},
	}}

	if got := (Gate{}).Check(ok); got != nil {
		t.Errorf("disabled gate = %v, want nil", got)
	}
	if (Gate{}).Enabled() || !(Gate{MinTPS: 1}).Enabled() || !(Gate{MaxTTFT: time.Second}).Enabled() {
		t.Error("Enabled should be true iff a threshold is set")
	}
	if got := (Gate{MinTPS: 50, MaxTTFT: time.Second}).Check(ok); len(got) != 0 {
		t.Errorf("passing gate = %v, want none (thresholds are inclusive)", got)
	}
	got := (Gate{MinTPS: 60, MaxTTFT: 400 * time.Millisecond}).Check(ok)
	want := []string{"TPS p50 50.0 < min 60.0", "TTFT p50 0.50s > max 0.40s"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("failing gate = %q, want %q", got, want)
	}
	failed := Summary{Streams: 2, Errors: []StreamError{{}, {}}}
	if got := (Gate{MinTPS: 1}).Check(failed); len(got) != 1 || got[0] != "no successful streams" {
		t.Errorf("all-failed gate = %q, want [no successful streams]", got)
	}

	nonStreamed := Summary{Results: []Result{{OutputTokens: 50, TotalWall: time.Second}}}
	if got := (Gate{MaxTTFT: time.Second}).Check(nonStreamed); len(got) != 1 || got[0] != "TTFT unavailable (non-streaming response)" {
		t.Errorf("non-streamed TTFT gate = %q, want [TTFT unavailable (non-streaming response)]", got)
	}
	if got := (Gate{MinTPS: 1}).Check(nonStreamed); len(got) != 0 {
		t.Errorf("non-streamed TPS-only gate = %q, want none (TPS falls back to e2e)", got)
	}
}
