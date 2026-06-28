package bench

import (
	"testing"
	"time"
)

func TestTPSUsesNMinusOneOverGenerationTime(t *testing.T) {
	// The first token is produced during TTFT, so the generation window spans
	// only N-1 token intervals. 100 tokens over 2s => 99/2 = 49.5 tok/s.
	r := Result{OutputTokens: 100, GenTime: 2 * time.Second, TotalWall: 4 * time.Second}
	if got := r.TPS(); got != 49.5 {
		t.Fatalf("TPS = %v, want 49.5", got)
	}
}

func TestTPSFallsBackToEndToEndForSingleToken(t *testing.T) {
	// With one token there is no generation interval to measure.
	r := Result{OutputTokens: 1, GenTime: 2 * time.Second, TotalWall: 4 * time.Second}
	if got := r.TPS(); got != 0.25 {
		t.Fatalf("TPS = %v, want 0.25", got)
	}
}

func TestTPSFallsBackToEndToEndWhenNoGenTime(t *testing.T) {
	r := Result{OutputTokens: 100, GenTime: 0, TotalWall: 4 * time.Second}
	if got := r.TPS(); got != 25 {
		t.Fatalf("TPS = %v, want 25", got)
	}
}

func TestEndToEndTPSZeroWallIsZero(t *testing.T) {
	r := Result{OutputTokens: 100, GenTime: 0, TotalWall: 0}
	if got := r.TPS(); got != 0 {
		t.Fatalf("TPS = %v, want 0", got)
	}
	if got := r.EndToEndTPS(); got != 0 {
		t.Fatalf("EndToEndTPS = %v, want 0", got)
	}
}
