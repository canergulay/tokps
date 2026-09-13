package bench

// ProgressEvent describes one completed batch. RunN emits one per warmup and
// measured batch through Config.Progress so callers can show live progress
// while a long benchmark runs.
type ProgressEvent struct {
	Phase       string  // "warmup" or "run"
	Index       int     // 1-based within the phase
	Total       int     // batches in the phase
	Concurrency int     // streams in the batch
	Label       string  // stamped by RunSweep ("c=4") / RunCompare (model); "" otherwise
	BatchTPS    float64 // aggregate tok/s of the batch; 0 for warmup
	Failed      int     // streams in the batch that errored
}

// progress emits ev when a Progress callback is configured.
func (c Config) progress(ev ProgressEvent) {
	if c.Progress != nil {
		c.Progress(ev)
	}
}

// withLabel returns a copy of cfg whose Progress callback stamps label on
// every event before forwarding it. It is a no-op when no callback is set.
func withLabel(cfg Config, label string) Config {
	if cfg.Progress == nil {
		return cfg
	}
	inner := cfg.Progress
	cfg.Progress = func(ev ProgressEvent) {
		ev.Label = label
		inner(ev)
	}
	return cfg
}
