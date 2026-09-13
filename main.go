// Command tokps benchmarks the token-generation throughput of an
// OpenAI-compatible /chat/completions endpoint.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/canergulay/tokps/internal/bench"
)

const defaultPrompt = "Write a detailed explanation of how TCP congestion control works, " +
	"covering slow start, congestion avoidance, fast retransmit, and fast recovery."

// defaultSweepLevels is used when --sweep is given without an explicit list.
const defaultSweepLevels = "1,2,4,8"

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

// sweepValue implements flag.Value with bool-flag semantics so that both a
// bare --sweep (default curve) and --sweep=1,2,4,8 work.
type sweepValue struct {
	enabled bool
	levels  []int
}

func (s *sweepValue) String() string {
	if !s.enabled {
		return ""
	}
	parts := make([]string, len(s.levels))
	for i, l := range s.levels {
		parts[i] = strconv.Itoa(l)
	}
	return strings.Join(parts, ",")
}

func (s *sweepValue) Set(v string) error {
	switch v {
	case "", "true":
		levels, err := bench.ParseLevels(defaultSweepLevels)
		if err != nil {
			return err
		}
		s.enabled = true
		s.levels = levels
		return nil
	case "false":
		s.enabled = false
		s.levels = nil
		return nil
	}
	levels, err := bench.ParseLevels(v)
	if err != nil {
		return err
	}
	s.enabled = true
	s.levels = levels
	return nil
}

// IsBoolFlag lets a bare --sweep be accepted with no value.
func (s *sweepValue) IsBoolFlag() bool { return true }

type options struct {
	url, model, apiKey, prompt       string
	maxTokens                        int
	maxTokensField                   string
	timeout                          time.Duration
	runs, warmup, concurrency        int
	sweep                            sweepValue
	detail, jsonOut, md, showVersion bool
	quiet                            bool
	costIn, costOut                  float64
	charsPerToken                    float64
	extraBody                        string
	minTPS                           float64
	maxTTFT                          time.Duration
}

// parseFlags defines and parses the CLI flags. It returns the parsed options,
// any leftover positional arguments, and a non-negative exit code when the
// program should stop immediately (help or a parse error).
func parseFlags(args []string, stderr io.Writer) (*options, []string, int) {
	fs := flag.NewFlagSet("tokps", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opts := &options{}

	fs.StringVar(&opts.url, "url", "", "Base URL of the OpenAI-compatible endpoint (required)")
	fs.StringVar(&opts.model, "model", "", "Model name (required); a comma-separated list benchmarks each and prints a comparison table")
	fs.StringVar(&opts.apiKey, "api-key", "", "API key (defaults to the API_KEY env var, then OPENAI_API_KEY)")
	fs.StringVar(&opts.prompt, "prompt", defaultPrompt, "Test prompt to send")
	fs.IntVar(&opts.maxTokens, "max-tokens", 512, "Maximum output tokens")
	fs.StringVar(&opts.maxTokensField, "max-tokens-field", "", "Request field for the output cap: max_tokens (default) or max_completion_tokens")
	fs.DurationVar(&opts.timeout, "timeout", 60*time.Second, "Per-request timeout")
	fs.IntVar(&opts.runs, "runs", 5, "Number of timed runs (reports p50 + min-max across them)")
	fs.IntVar(&opts.warmup, "warmup", 1, "Number of discarded warmup runs before measuring")
	fs.IntVar(&opts.concurrency, "concurrency", 1, "Parallel streams per run (>1 reports aggregate tok/s under load)")
	fs.Var(&opts.sweep, "sweep", "Sweep concurrency levels: bare --sweep = 1,2,4,8, or --sweep=1,2,4,8")
	fs.BoolVar(&opts.detail, "detail", false, "Show extra detail (inter-chunk latency p50/p95)")
	fs.BoolVar(&opts.jsonOut, "json", false, "Emit machine-readable JSON instead of the text summary")
	fs.BoolVar(&opts.md, "md", false, "Emit a GitHub-flavored markdown table instead of the text summary")
	fs.BoolVar(&opts.showVersion, "version", false, "Print version and exit")
	fs.Float64Var(&opts.costIn, "cost-in", 0, "Input token price in USD per 1M tokens (adds a per-request cost line)")
	fs.Float64Var(&opts.costOut, "cost-out", 0, "Output token price in USD per 1M tokens (adds a per-request cost line)")
	fs.Float64Var(&opts.charsPerToken, "chars-per-token", 4, "Chars-per-token ratio for the estimated fallback (CJK ~ 1.5-2)")
	fs.StringVar(&opts.extraBody, "extra-body", "", "JSON object merged into the request body, e.g. '{\"temperature\":0}' (your keys win)")
	fs.BoolVar(&opts.quiet, "quiet", false, "Suppress progress and warnings on stderr (errors are still printed)")
	fs.Float64Var(&opts.minTPS, "min-tps", 0, "CI gate: exit 3 if generation TPS p50 is below this (tok/s)")
	fs.DurationVar(&opts.maxTTFT, "max-ttft", 0, "CI gate: exit 3 if TTFT p50 exceeds this (e.g. 1.5s)")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return opts, nil, 0
		}
		return opts, nil, 2
	}
	return opts, fs.Args(), -1
}

func looksLikeLevels(s string) bool {
	_, err := bench.ParseLevels(s)
	return err == nil
}

// isTerminal reports whether w is an interactive terminal (a character
// device), so progress lines reach humans but stay out of CI logs and pipes.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

// progressLine formats one batch's progress for stderr.
func progressLine(ev bench.ProgressEvent) string {
	var b strings.Builder
	if ev.Label != "" {
		fmt.Fprintf(&b, "%s  ", ev.Label)
	}
	fmt.Fprintf(&b, "%s %d/%d", ev.Phase, ev.Index, ev.Total)
	if ev.Phase == "run" {
		fmt.Fprintf(&b, "   %.1f tok/s", ev.BatchTPS)
		if ev.Concurrency > 1 {
			fmt.Fprintf(&b, " (aggregate, %d streams)", ev.Concurrency)
		}
		if ev.Failed > 0 {
			fmt.Fprintf(&b, "   %d failed", ev.Failed)
		}
	}
	return b.String()
}

func run(args []string, stdout, stderr io.Writer) int {
	opts, leftovers, code := parseFlags(args, stderr)
	if code >= 0 {
		return code
	}
	if opts.showVersion {
		fmt.Fprintln(stdout, "tokps", version)
		return 0
	}
	if opts.url == "" || opts.model == "" {
		fmt.Fprintln(stderr, "error: --url and --model are required")
		return 2
	}
	models, err := bench.ParseModels(opts.model)
	if err != nil {
		fmt.Fprintf(stderr, "error: --model: %v\n", err)
		return 2
	}
	if len(models) > 1 && opts.sweep.enabled {
		fmt.Fprintln(stderr, "error: --sweep and a multi-model --model cannot be combined")
		return 2
	}
	if opts.md && opts.jsonOut {
		fmt.Fprintln(stderr, "error: --md and --json cannot be combined")
		return 2
	}
	if len(leftovers) > 0 {
		if looksLikeLevels(leftovers[0]) {
			fmt.Fprintf(stderr, "error: unexpected argument %q — use --sweep=%s\n", leftovers[0], leftovers[0])
		} else {
			fmt.Fprintf(stderr, "error: unexpected argument %q\n", leftovers[0])
		}
		return 2
	}
	switch opts.maxTokensField {
	case "", "max_tokens", "max_completion_tokens":
	default:
		fmt.Fprintf(stderr, "error: --max-tokens-field must be \"max_tokens\" or \"max_completion_tokens\", got %q\n", opts.maxTokensField)
		return 2
	}
	if opts.costIn < 0 || opts.costOut < 0 {
		fmt.Fprintln(stderr, "error: --cost-in and --cost-out must be >= 0")
		return 2
	}
	if opts.charsPerToken <= 0 {
		fmt.Fprintln(stderr, "error: --chars-per-token must be > 0")
		return 2
	}
	if opts.timeout <= 0 {
		fmt.Fprintln(stderr, "error: --timeout must be > 0")
		return 2
	}
	if opts.minTPS < 0 || opts.maxTTFT < 0 {
		fmt.Fprintln(stderr, "error: --min-tps and --max-ttft must be >= 0")
		return 2
	}

	var extra map[string]any
	if opts.extraBody != "" {
		if err := json.Unmarshal([]byte(opts.extraBody), &extra); err != nil || extra == nil {
			fmt.Fprintf(stderr, "error: --extra-body must be a JSON object, got %q\n", opts.extraBody)
			return 2
		}
	}

	key := opts.apiKey
	if key == "" {
		key = os.Getenv("API_KEY")
	}
	if key == "" {
		key = os.Getenv("OPENAI_API_KEY")
	}

	cfg := bench.Config{
		URL:            opts.url,
		Model:          models[0],
		APIKey:         key,
		Prompt:         opts.prompt,
		MaxTokens:      opts.maxTokens,
		MaxTokensField: opts.maxTokensField,
		Timeout:        opts.timeout,
		CostIn:         opts.costIn,
		CostOut:        opts.costOut,
		CharsPerToken:  opts.charsPerToken,
		ExtraBody:      extra,
	}

	if !opts.quiet {
		cfg.Warnf = func(format string, args ...any) {
			fmt.Fprintf(stderr, "warning: "+format+"\n", args...)
		}
		if isTerminal(stderr) {
			cfg.Progress = func(ev bench.ProgressEvent) { fmt.Fprintln(stderr, progressLine(ev)) }
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	gate := bench.Gate{MinTPS: opts.minTPS, MaxTTFT: opts.maxTTFT}

	if opts.sweep.enabled {
		sums, err := bench.RunSweep(ctx, cfg, opts.runs, opts.warmup, opts.sweep.levels)
		if err != nil {
			return reportErr(err, stderr, opts.runs)
		}
		if err := writeSweep(stdout, sums, opts); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if checkGates(gate, sums, levelLabel, stderr) {
			return exitGateFailed
		}
		return 0
	}

	if len(models) > 1 {
		sums, err := bench.RunCompare(ctx, cfg, models, opts.runs, opts.warmup, opts.concurrency)
		if err != nil {
			return reportErr(err, stderr, opts.runs)
		}
		if err := writeCompare(stdout, sums, opts); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if checkGates(gate, sums, modelLabel, stderr) {
			return exitGateFailed
		}
		return 0
	}

	sum, err := bench.RunN(ctx, cfg, opts.runs, opts.warmup, opts.concurrency)
	if err != nil {
		return reportErr(err, stderr, opts.runs)
	}
	if err := writeSummary(stdout, sum, opts); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if checkGates(gate, []bench.Summary{sum}, noLabel, stderr) {
		return exitGateFailed
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
