// Command tokps benchmarks the token-generation throughput of an
// OpenAI-compatible /chat/completions endpoint.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/canergulay/tokps/internal/bench"
	"github.com/canergulay/tokps/internal/report"
)

const defaultPrompt = "Write a detailed explanation of how TCP congestion control works, " +
	"covering slow start, congestion avoidance, fast retransmit, and fast recovery."

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	url := flag.String("url", "", "Base URL of the OpenAI-compatible endpoint (required)")
	model := flag.String("model", "", "Model name (required)")
	apiKey := flag.String("api-key", "", "API key (defaults to the API_KEY env var, then OPENAI_API_KEY)")
	prompt := flag.String("prompt", defaultPrompt, "Test prompt to send")
	maxTokens := flag.Int("max-tokens", 512, "Maximum output tokens")
	timeout := flag.Duration("timeout", 60*time.Second, "Per-request timeout")
	runs := flag.Int("runs", 5, "Number of timed runs (reports p50 + min–max across them)")
	warmup := flag.Int("warmup", 1, "Number of discarded warmup runs before measuring")
	detail := flag.Bool("detail", false, "Show extra detail (inter-token latency p50/p95)")
	jsonOut := flag.Bool("json", false, "Emit machine-readable JSON instead of the text summary")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("tokps", version)
		return
	}

	if *url == "" || *model == "" {
		fmt.Fprintln(os.Stderr, "error: --url and --model are required")
		flag.Usage()
		os.Exit(2)
	}

	key := *apiKey
	if key == "" {
		key = os.Getenv("API_KEY")
	}
	if key == "" {
		key = os.Getenv("OPENAI_API_KEY")
	}

	cfg := bench.Config{
		URL:       *url,
		Model:     *model,
		APIKey:    key,
		Prompt:    *prompt,
		MaxTokens: *maxTokens,
		Timeout:   *timeout,
	}

	sum, err := bench.RunN(context.Background(), cfg, *runs, *warmup)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if *jsonOut {
		if err := report.FormatJSON(os.Stdout, sum); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	report.FormatSummary(os.Stdout, sum, *detail)
}
