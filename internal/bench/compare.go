package bench

import (
	"context"
	"fmt"
	"strings"
)

// ParseModels splits a comma-separated --model value into trimmed names. A
// single name is the ordinary (non-compare) case; empty entries are an error.
func ParseModels(s string) ([]string, error) {
	parts := strings.Split(s, ",")
	models := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("empty model name in %q", s)
		}
		models = append(models, p)
	}
	return models, nil
}

// RunCompare benchmarks each model in turn against the same endpoint and
// returns one Summary per model, in input order, sharing one connection pool.
//
// The first model is the canary: any error there aborts, so auth and URL
// problems fail fast. A later model that fails — a typo'd name (404), a 429 —
// is kept as a failed Summary so the comparison still shows the others.
// Interruption always aborts.
func RunCompare(ctx context.Context, cfg Config, models []string, runs, warmup, concurrency int) ([]Summary, error) {
	if cfg.Client == nil {
		cfg.Client = poolingClient(max(concurrency, 1))
	}
	sums := make([]Summary, 0, len(models))
	for i, m := range models {
		mcfg := withLabel(cfg, m)
		mcfg.Model = m
		s, err := RunN(ctx, mcfg, runs, warmup, concurrency)
		if err != nil && (i == 0 || isInterrupted(err)) {
			return nil, fmt.Errorf("model %s: %w", m, err)
		}
		sums = append(sums, s)
	}
	return sums, nil
}
