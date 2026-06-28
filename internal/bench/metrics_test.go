package bench

import (
	"testing"
	"time"
)

func TestTPSUsesGenerationTime(t *testing.T) {
	r := Result{OutputTokens: 100, GenTime: 2 * time.Second, TotalWall: 4 * time.Second}
	if got := r.TPS(); got != 50 {
		t.Fatalf("TPS = %v, want 50", got)
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
