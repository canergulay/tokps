// Command tokencounter benchmarks the token-generation throughput of an
// OpenAI-compatible /chat/completions endpoint.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"tokencounter/internal/bench"
	"tokencounter/internal/report"
)

const defaultPrompt = "Write a detailed explanation of how TCP congestion control works, " +
	"covering slow start, congestion avoidance, fast retransmit, and fast recovery."

func main() {
	url := flag.String("url", "", "Base URL of the OpenAI-compatible endpoint (required)")
	model := flag.String("model", "", "Model name (required)")
	apiKey := flag.String("api-key", "", "API key (defaults to the API_KEY env var, then OPENAI_API_KEY)")
	prompt := flag.String("prompt", defaultPrompt, "Test prompt to send")
	maxTokens := flag.Int("max-tokens", 512, "Maximum output tokens")
	timeout := flag.Duration("timeout", 60*time.Second, "Whole-request timeout")
	flag.Parse()

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

	res, err := bench.Run(context.Background(), cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	report.Format(os.Stdout, res)
}
