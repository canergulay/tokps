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
	// MaxErrorRate is the highest tolerated share of failed measured
	// streams, in [0,1]; nil disables it, so 0 means "no failures allowed".
	MaxErrorRate *float64
}

// Enabled reports whether any threshold is set.
func (g Gate) Enabled() bool { return g.MinTPS > 0 || g.MaxTTFT > 0 || g.MaxErrorRate != nil }

// Check returns one human-readable failure per violated threshold, or an
// empty slice when s passes. Thresholds are inclusive. A summary with no
// successful stream fails outright, and a TTFT threshold fails when the
// response was not streamed.
func (g Gate) Check(s Summary) []string {
	if !g.Enabled() {
		return nil
	}
	if s.AllFailed() {
		return []string{"no successful streams"}
	}
	var fails []string
	if g.MaxErrorRate != nil {
		if rate := s.ErrorRate(); rate > *g.MaxErrorRate {
			fails = append(fails, fmt.Sprintf("error rate %s (%d/%d streams) > max %s",
				pct(rate), s.Failed(), s.StreamCount(), pct(*g.MaxErrorRate)))
		}
	}
	if g.MinTPS > 0 {
		if tps := s.GenTPS().P50; tps < g.MinTPS {
			fails = append(fails, fmt.Sprintf("TPS p50 %.1f < min %.1f", tps, g.MinTPS))
		}
	}
	if g.MaxTTFT > 0 {
		switch {
		case !s.Streamed():
			fails = append(fails, "TTFT unavailable (non-streaming response)")
		default:
			if ttft, limit := s.TTFT().P50, g.MaxTTFT.Seconds(); ttft > limit {
				fails = append(fails, fmt.Sprintf("TTFT p50 %.2fs > max %.2fs", ttft, limit))
			}
		}
	}
	return fails
}

// pct renders a [0,1] ratio as a percentage, e.g. 0.125 → "12.5%".
func pct(v float64) string { return fmt.Sprintf("%.1f%%", v*100) }
