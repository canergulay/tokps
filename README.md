# tokencounter

Single-shot CLI that measures token-generation throughput (TPS) of any
OpenAI-compatible `/chat/completions` endpoint.

## Build

```sh
go build -o tokencounter .
```

## Usage

```sh
export API_KEY=sk-...
./tokencounter --url https://api.openai.com/v1 --model gpt-4o-mini
./tokencounter --url https://open.bigmodel.cn/api/paas/v4 --model glm-4-flash
```

`API_KEY` is provider-agnostic — set it once and it applies to whatever `--url`
you point at.

| Flag | Default | Description |
|------|---------|-------------|
| `--url` | (required) | Base URL; `/chat/completions` is appended automatically. |
| `--model` | (required) | Model name. |
| `--api-key` | `API_KEY` env, then `OPENAI_API_KEY` | Bearer token. |
| `--prompt` | built-in | Override the test prompt. |
| `--max-tokens` | 512 | Upper bound on output length. |
| `--timeout` | 60s | Request timeout. |

The headline **TPS** is output tokens ÷ generation time (excludes time to
first token); **end-to-end** includes TTFT. Token counts are taken from the
stream's `usage` field when present (labeled `exact`) and estimated from
streamed chunks otherwise (labeled `estimated`).
