# tokencounter

[![Go Reference](https://pkg.go.dev/badge/github.com/canergulay/tokencounter.svg)](https://pkg.go.dev/github.com/canergulay/tokencounter)
[![Go Version](https://img.shields.io/badge/go-1.23%2B-00ADD8?logo=go)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

A tiny, single-shot CLI that measures the **token-generation throughput (TPS)**
of any OpenAI-compatible `/chat/completions` endpoint — OpenAI, Z.ai / GLM,
local models, custom gateways, anything that speaks the same wire format.

You run it once, it sends a test prompt with streaming on, watches the tokens
stream back, and prints how fast the model generated them.

```text
tokencounter — glm-5.2 @ api.z.ai

  prompt tokens     39
  output tokens     200   (exact)
  time to first     2.68 s
  generation        2.72 s
  total wall        5.40 s

  TPS               73.6 tok/s   (generation)
  end-to-end        37.0 tok/s   (incl. TTFT)
```

## Install

```sh
go install github.com/canergulay/tokencounter@latest
```

This drops a `tokencounter` binary in `$(go env GOPATH)/bin`. Make sure that's
on your `PATH`.

Or build from source:

```sh
git clone https://github.com/canergulay/tokencounter
cd tokencounter
go build -o tokencounter .
```

## Usage

Set your key once (`API_KEY` is provider-agnostic — it applies to whatever
`--url` you point at), then run:

```sh
export API_KEY=sk-...

# OpenAI
tokencounter --url https://api.openai.com/v1 --model gpt-4o-mini

# Z.ai / GLM
tokencounter --url https://api.z.ai/api/paas/v4 --model glm-5.2

# A local OpenAI-compatible server (e.g. llama.cpp, vLLM, Ollama)
tokencounter --url http://localhost:8080/v1 --model my-local-model
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
| `--timeout` | `60s` | Whole-request timeout. |

Run `tokencounter` with no flags to see the full list.

## How it measures

The request is sent with `stream: true` and `stream_options.include_usage:
true`. As chunks arrive, tokencounter records:

- **time to first token (TTFT)** — request send → first generated token.
- **generation time** — first → last generated token.
- **total wall** — request send → last token.

It reports two throughput numbers:

- **TPS (headline)** = output tokens ÷ generation time. This is the pure
  generation rate, excluding the initial latency.
- **end-to-end** = output tokens ÷ total wall, which folds in TTFT.

**Token counts** come from the stream's `usage` field when the server sends it
(labeled `exact`). If it doesn't, tokencounter falls back to counting streamed
chunks (labeled `estimated`).

### Reasoning models

Reasoning models such as **GLM-5.2** and DeepSeek-R1 stream their thinking
tokens in `delta.reasoning_content` rather than `delta.content`, often by
default. tokencounter counts those toward timing and throughput, so TTFT and
TPS reflect the full generation including the reasoning phase.

### Non-streaming endpoints

If a server ignores `stream: true` and returns a single JSON object,
tokencounter still reports tokens (from `usage`) and total time; TTFT and the
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
