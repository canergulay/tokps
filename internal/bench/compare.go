package bench

import (
	"cmp"
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

// Target is one endpoint in a comparison: a model, the base URL serving it,
// and the key to use there. RunCompare fills an empty URL or APIKey from the
// shared Config.
type Target struct {
	Model  string
	URL    string
	APIKey string
}

// ModelTargets turns a plain model list into targets on the shared endpoint.
func ModelTargets(models []string) []Target {
	ts := make([]Target, len(models))
	for i, m := range models {
		ts[i] = Target{Model: m}
	}
	return ts
}

// TargetLabels names each target for progress lines and gate failures: just
// the model when every target shares one host, "model@host" otherwise.
func TargetLabels(cfg Config, targets []Target) []string {
	hosts := map[string]bool{}
	for _, t := range targets {
		hosts[hostOf(cmp.Or(t.URL, cfg.URL))] = true
	}
	labels := make([]string, len(targets))
	for i, t := range targets {
		labels[i] = t.Model
		if len(hosts) > 1 {
			labels[i] = t.Model + "@" + hostOf(cmp.Or(t.URL, cfg.URL))
		}
	}
	return labels
}

// RunCompare benchmarks every target and returns one Summary per target, in
// input order, sharing one connection pool.
//
// All warmups run first; then the measured runs are interleaved round-robin
// (A,B,C, B,C,A, …) rather than all of A before all of B, so load drift on a
// shared endpoint — or on your own network — hits every target equally.
//
// The first target is the canary: any error there aborts, so auth and URL
// problems fail fast. A later target that fails — a typo'd name (404), a 429
// — is kept as a failed Summary so the comparison still shows the others.
// Interruption always aborts.
func RunCompare(ctx context.Context, cfg Config, targets []Target, runs, warmup, concurrency int) ([]Summary, error) {
	if cfg.Client == nil {
		cfg.Client = poolingClient(max(concurrency, 1))
	}
	labels := TargetLabels(cfg, targets)
	rs := make([]*runner, len(targets))
	live := make([]bool, len(targets))
	for i, t := range targets {
		tcfg := withLabel(cfg, labels[i])
		tcfg.Model = t.Model
		tcfg.URL = cmp.Or(t.URL, cfg.URL)
		tcfg.APIKey = cmp.Or(t.APIKey, cfg.APIKey)
		rs[i] = newRunner(tcfg, runs, warmup, concurrency)
		err := rs[i].warm(ctx, i > 0)
		if err != nil && (i == 0 || isInterrupted(err)) {
			return nil, fmt.Errorf("%s: %w", labels[i], err)
		}
		live[i] = err == nil
	}
	for round := range rs[0].runs {
		for k := range rs {
			i := (round + k) % len(rs)
			if !live[i] {
				continue
			}
			if err := rs[i].measure(ctx); err != nil {
				return nil, fmt.Errorf("%s: %w", labels[i], err)
			}
			// Without warmup, the canary's first batch is the fail-fast
			// check: stop before paying for every other target.
			if round == 0 && i == 0 {
				if _, err := rs[0].result(); err != nil {
					return nil, fmt.Errorf("%s: %w", labels[0], err)
				}
			}
		}
	}
	sums := make([]Summary, len(rs))
	for i, r := range rs {
		s, err := r.result()
		if err != nil && i == 0 {
			return nil, fmt.Errorf("%s: %w", labels[i], err)
		}
		sums[i] = s
	}
	return sums, nil
}
