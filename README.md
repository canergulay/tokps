<p align="center">
  <img src="assets/logo.png" alt="tokps logo" width="128" height="128">
</p>

<h1 align="center">tokps</h1>

<p align="center">
  <a href="https://pkg.go.dev/github.com/canergulay/tokps"><img src="https://pkg.go.dev/badge/github.com/canergulay/tokps.svg" alt="Go Reference"></a>
  <a href="https://go.dev/"><img src="https://img.shields.io/badge/go-1.23%2B-00ADD8?logo=go" alt="Go Version"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-green.svg" alt="License: MIT"></a>
</p>

A tiny CLI that measures the **token-generation throughput (TPS)** of any
OpenAI-compatible `/chat/completions` endpoint — OpenAI, Z.ai / GLM, local
models, custom gateways, anything that speaks the same wire format.

It sends a test prompt with streaming on, watches the tokens stream back, and
prints how fast the model generated them. By default it runs one discarded
warmup followed by five timed requests and reports the **median (p50) plus the
observed min–max range**, so a single cold start or network hiccup doesn't skew
the result.

```text
tokps — glm-5.2 @ api.z.ai  (5 runs, 1 warmup)

  prompt tokens     39
  output tokens     200   (exact, median)

  TTFT     p50 2.61s   range 2.41s–2.95s
  TPS      p50 73.1   range 69.8–75.4   (generation, N-1)
  e2e      p50 36.8   range 34.1–38.0   (incl. TTFT)
```

For a single cheap request, pass `--runs 1 --warmup 0` — the output falls back
to a detailed single-shot block (per-run TTFT, generation, and total wall).

## Install

```sh
go install github.com/canergulay/tokps@latest
```

This drops a `tokps` binary in `$(go env GOPATH)/bin`. Make sure that's
on your `PATH`.

Or build from source:

```sh
git clone https://github.com/canergulay/tokps
cd tokps
go build -o tokps .
```

## Usage

Set your key once (`API_KEY` is provider-agnostic — it applies to whatever
`--url` you point at), then run:

```sh
export API_KEY=sk-...

# OpenAI
tokps --url https://api.openai.com/v1 --model gpt-4o-mini

# Z.ai / GLM
tokps --url https://api.z.ai/api/paas/v4 --model glm-5.2

# A local OpenAI-compatible server (e.g. llama.cpp, vLLM, Ollama)
tokps --url http://localhost:8080/v1 --model my-local-model
```

The base URL has `/chat/completions` appended automatically, so `…/v1` and
`…/paas/v4` both work. If you pass a full `…/chat/completions` URL it's used
as-is.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--url` | *(required)* | Base URL of the endpoint. |
| `--model` | *(required)* | Model name. |
| `--api-key` | `API_KEY` env, then `OPENAI_API_KEY` | Bearer token. Flag wins over env. |
| `--prompt` | built-in | Override the test prompt. |
| `--max-tokens` | `512` | Upper bound on output length. |
| `--runs` | `5` | Number of timed runs; reports p50 + min–max across them. |
| `--warmup` | `1` | Discarded warmup runs before measuring (absorbs cold start). |
| `--detail` | `false` | Also show inter-token latency (ITL) p50/p95. |
| `--json` | `false` | Emit machine-readable JSON instead of the text summary. |
| `--concurrency` | `1` | Parallel streams per run; >1 reports aggregate tok/s under load. |
| `--sweep` | — | Comma-separated concurrency levels to sweep, e.g. `1,2,4,8`. |
| `--timeout` | `60s` | Per-request timeout. |

> Each invocation sends `--warmup` + `--runs` requests (6 by default), so it
> makes that many billable calls against a metered endpoint. Use
> `--runs 1 --warmup 0` for a single request.

Run `tokps` with no flags to see the full list.

## How it measures

Each request is sent with `stream: true` and `stream_options.include_usage:
true`. As chunks arrive, tokps records:

- **time to first token (TTFT)** — request send → first generated token.
- **generation time** — first → last generated token.
- **total wall** — request send → last token.

It reports two throughput numbers:

- **TPS (headline)** = `(output tokens − 1) ÷ generation time`. The first token
  is produced during TTFT, so the first-to-last window spans *N − 1* token
  intervals — dividing by `N − 1` is the standard serving-benchmark definition
  (vLLM, NVIDIA genai-perf, Anyscale llmperf) and the inverse of mean
  inter-token latency. This is the pure decode rate, excluding initial latency.
- **end-to-end** = `output tokens ÷ total wall`, which folds in TTFT.

**Runs and percentiles.** A model's speed varies run to run (cold replicas,
queueing, KV-cache state, network jitter). So tokps runs `--warmup`
discarded requests, then `--runs` timed ones, and reports the **median (p50)**
and the **observed min–max range** for each metric. Min/max are reported rather
than p90/p99 because they're unambiguous for both latency (higher = worse) and
throughput (higher = better), and don't oversell percentile resolution at small
run counts.

**Token counts** come from the stream's `usage` field when the server sends it
(labeled `exact`). If a server omits it, tokps estimates from the streamed
text length at ~4 chars/token (labeled `estimated`) — model-agnostic and
independent of how the server chose to chunk the stream.

### Detail and JSON output

`--detail` adds an **inter-token latency (ITL)** line — the p50 and p95 of the
gaps between successive streamed tokens, pooled across runs. p95 surfaces
stalls/jitter that a single averaged rate hides.

`--json` emits the full result as machine-readable JSON instead of the text
table — the p50/min/max for every metric, ITL, and a `runs_detail` array with
each run's raw numbers — for CI gates, storing, and diffing over time.

### Concurrency and load

By default tokps measures a single stream — the "how fast is one response"
question. `--concurrency N` instead fires **N streams in parallel** per run and
adds an **aggregate tok/s** line (total output tokens across all streams ÷ wall
time) alongside the per-stream TTFT/TPS distribution — i.e. throughput under
load.

`--sweep 1,2,4,8` runs the benchmark at each level in turn and prints the
**throughput-vs-concurrency curve**, so you can see where an endpoint saturates:

```text
tokps — glm-5.2 @ api.z.ai  (sweep, 3 runs, 1 warmup)

  concurrency   aggregate tok/s   TTFT p50   TPS p50/stream
  1             73.1              0.42s      73.1
  2             140.0             0.45s      70.0
  4             250.0             0.61s      62.5
  8             300.0             1.10s      37.5
```

### Reasoning models

Reasoning models such as **GLM-5.2** and DeepSeek-R1 stream their thinking
tokens in `delta.reasoning_content` rather than `delta.content`, often by
default. tokps counts those toward timing and throughput, so TTFT and
TPS reflect the full generation including the reasoning phase.

### Non-streaming endpoints

If a server ignores `stream: true` and returns a single JSON object,
tokps still reports tokens (from `usage`) and total time; TTFT and the
generation-only rate are shown as `n/a` since per-token timing isn't available.

## Development

Pure Go standard library, no third-party dependencies.

```sh
go test ./...     # unit + integration tests (httptest, no network)
go vet ./...
go build ./...
```

The code is split into small, independently testable packages:

- `internal/sse` — turns an SSE byte stream into data payloads.
- `internal/bench` — builds/sends the request, drives the parser, collects metrics.
- `internal/report` — formats the summary block.

## License

[MIT](LICENSE) © Caner Gulay
