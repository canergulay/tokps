package bench

import (
	"fmt"
	"time"
)

// Gate holds CI thresholds checked against a Summary's medians. A zero value
// disables that check.
type Gate struct {
	MinTPS  float64       // minimum generation TPS p50 (tok/s)
	MaxTTFT time.Duration // maximum TTFT p50
}

// Enabled reports whether any threshold is set.
func (g Gate) Enabled() bool { return g.MinTPS > 0 || g.MaxTTFT > 0 }

// Check returns one human-readable failure per violated threshold, or an
// empty slice when s passes. Thresholds are inclusive. A summary with no
// successful stream fails outright.
func (g Gate) Check(s Summary) []string {
	if !g.Enabled() {
		return nil
	}
	if s.AllFailed() {
		return []string{"no successful streams"}
	}
	var fails []string
	if g.MinTPS > 0 {
		if tps := s.GenTPS().P50; tps < g.MinTPS {
			fails = append(fails, fmt.Sprintf("TPS p50 %.1f < min %.1f", tps, g.MinTPS))
		}
	}
	if g.MaxTTFT > 0 {
		if ttft, limit := s.TTFT().P50, g.MaxTTFT.Seconds(); ttft > limit {
			fails = append(fails, fmt.Sprintf("TTFT p50 %.2fs > max %.2fs", ttft, limit))
		}
	}
	return fails
}
