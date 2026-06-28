# tokps — OpenAI-compatible TPS benchmark CLI

**Date:** 2026-06-28
**Status:** Approved design

## Summary

`tokps` is a single-shot Go CLI that measures the token-generation
throughput (tokens per second) of any OpenAI-compatible `/chat/completions`
endpoint. You run it once; it sends a test prompt with streaming enabled,
watches the tokens stream back, prints a TPS summary, and exits.

The original motivation was measuring throughput while running LLM coding tools
(Claude Code, Codex, opencode, etc.), but the tool is a **direct benchmark
probe**, not a passive proxy: it sends its own prompt and measures the endpoint
directly. This keeps it universal across any OpenAI-compatible URL (OpenAI,
GLM/Zhipu, local models, custom gateways).

## Goals

- Measure real generation TPS of an OpenAI-compatible endpoint in one command.
- Work with any OpenAI-compatible `/chat/completions` URL and model.
- Report the numbers people actually compare: TPS, time-to-first-token (TTFT),
  output token count, durations.
- Use authoritative token counts when the server provides them; degrade
  gracefully when it doesn't.

## Non-goals (YAGNI)

- No passive proxy in front of real tools.
- No averaging over N runs (single shot only).
- No config file.
- No support for non-OpenAI request formats (e.g. Anthropic Messages) in v1.

## Usage

```sh
export OPENAI_API_KEY=sk-...
tokps --url https://api.openai.com/v1 --model gpt-4o-mini
tokps --url https://open.bigmodel.cn/api/paas/v4 --model glm-4-flash
```

### Flags

| Flag           | Required | Default                     | Notes |
|----------------|----------|-----------------------------|-------|
| `--url`        | yes      | —                           | Base URL; `/chat/completions` is appended. A full `…/chat/completions` URL is used as-is. |
| `--model`      | yes      | —                           | Model name passed in the request body. |
| `--api-key`    | no       | `API_KEY` env, then `OPENAI_API_KEY` | Sent as `Authorization: Bearer <key>`. Provider-agnostic `API_KEY` is preferred so one variable works for any `--url`; `OPENAI_API_KEY` is a fallback. Flag overrides env. Empty is allowed (some local servers need no key). |
| `--prompt`     | no       | built-in default prompt     | Overrides the default test prompt. |
| `--max-tokens` | no       | 512                         | Upper bound on output so the response is long enough for a meaningful TPS. |
| `--timeout`    | no       | 60s                         | Whole-request timeout. |

The built-in default prompt asks for a longish, deterministic-ish response
(e.g. "Write a detailed explanation of how TCP congestion control works.") so
that generation runs long enough to produce a stable TPS reading.

## How it measures

The request body sets:

```json
{
  "model": "<model>",
  "messages": [{"role": "user", "content": "<prompt>"}],
  "max_tokens": <max-tokens>,
  "stream": true,
  "stream_options": {"include_usage": true}
}
```

### Token counting

- **Exact:** if the stream's final chunk carries `usage.completion_tokens`, that
  is used as the authoritative output token count (labeled *exact*).
- **Estimated fallback:** if the server never sends `usage`, count the number of
  streamed chunks that contained non-empty `choices[].delta.content` and use
  that as an approximation (labeled *estimated*).

### Timing

Three timestamps are recorded:

- `tSend` — just before the request is written.
- `tFirst` — arrival of the first chunk containing non-empty content.
- `tLast` — arrival of the last content chunk.

### Derived metrics

- **TTFT** = `tFirst − tSend`.
- **Generation time** = `tLast − tFirst`.
- **Total wall** = `tLast − tSend` (falls back to request-completion time if no
  content arrived).
- **TPS (headline)** = `outputTokens ÷ generationTime` — pure generation rate,
  excluding initial latency. This is the headline number.
- **End-to-end TPS** = `outputTokens ÷ totalWall` — includes TTFT, reported as a
  secondary number.
- **Prompt tokens** = `usage.prompt_tokens` when available.

If generation time is zero (single-chunk response), TPS falls back to the
end-to-end figure to avoid divide-by-zero.

## Architecture

Small, independently testable units:

- **`main.go`** — flag parsing, config resolution (flags + env), wiring,
  human-readable error reporting, exit codes (0 success, non-zero on error).
- **`internal/sse`** — turns an SSE byte stream (`io.Reader`) into a sequence of
  raw data payloads. Handles `data: ` prefixes, the `[DONE]` sentinel, blank
  lines between events, and skips malformed lines. Pure and unit-tested with
  canned byte streams.
- **`internal/bench`** — owns the benchmark:
  - builds the chat-completions request,
  - sends it with the configured HTTP client/timeout,
  - drives `internal/sse` over the response body,
  - parses each chunk's JSON (`choices[].delta.content`, `usage`),
  - collects metrics into a `Result` struct.
  Tested against an `httptest.Server` emitting scripted SSE.
- **`internal/report`** — formats a `Result` into the summary block.

### Data flow

```
main → bench.Run(Config) ─ HTTP POST ─▶ endpoint
                          ◀─ SSE body ─ internal/sse → JSON chunks → metrics
       ◀─ Result ─────────
report.Format(Result) → stdout
```

## Error handling

- **Non-2xx response:** read the body, print status code + body, exit non-zero.
- **Network / timeout:** print a clear error, exit non-zero.
- **Non-streaming response** (server ignored `stream:true` and returned one JSON
  object — detected by content-type not being an event stream): parse it as a
  normal chat-completions response, use `usage` + total request time. TTFT and
  generation-only TPS are reported as not-available in that case.
- **Malformed SSE lines:** skipped, not fatal.
- **Missing required flags:** print usage, exit non-zero.

## Example output

```
tokps — glm-4-flash @ open.bigmodel.cn

  prompt tokens     14
  output tokens     487   (exact)
  time to first     0.42 s
  generation        6.31 s
  total wall        6.73 s

  TPS               77.2 tok/s   (generation)
  end-to-end        72.4 tok/s   (incl. TTFT)
```

When tokens are estimated the `output tokens` line is labeled `(estimated)`,
and when usage/TTFT are unavailable (non-streaming fallback) those lines read
`n/a`.

## Testing strategy

TDD on the two pure units, then integration:

1. **`internal/sse`** — feed canned multi-event byte streams (including split
   lines, blank lines, `[DONE]`, and garbage lines); assert the emitted
   payloads.
2. **`internal/bench` metrics** — assert TTFT / generation / TPS math given
   known timestamps and token counts, including the divide-by-zero fallback and
   the estimated-tokens fallback.
3. **Integration** — drive `bench.Run` against an `httptest.Server` scripted to
   emit a realistic SSE stream:
   - happy path with `usage` present,
   - no-usage fallback path,
   - HTTP error response,
   - non-streaming JSON response fallback.

No real API access is required for the test suite.

## Project layout

```
tokps/
  go.mod
  main.go
  internal/
    sse/
      parser.go
      parser_test.go
    bench/
      bench.go
      metrics.go
      bench_test.go
    report/
      report.go
      report_test.go
  docs/superpowers/specs/2026-06-28-tokps-design.md
```
