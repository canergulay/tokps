# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Cross-provider compare: `--target model@url` (repeatable) benchmarks models
  on different endpoints side by side — DeepSeek vs OpenAI vs your own vLLM in
  one table. Append `#ENV_VAR` to name the key variable for a target.
- Provider key discovery: tokps reads the provider's own key variable for
  well-known hosts (`DEEPSEEK_API_KEY` for api.deepseek.com, `OPENAI_API_KEY`
  for api.openai.com, `GROQ_API_KEY`, `OPENROUTER_API_KEY`, and more), so
  most setups need no key flags at all.
- Reasoning-model split: when a model thinks, the summary shows thinking vs
  answer tokens and an `answer` line — time to the first answer token, which
  is what a user actually waits for. `--json` gains a `reasoning` object and
  per-run `reasoning_tokens` / `ttfa_s`; compare tables gain an `answer p50`
  column. A run whose thinking used the whole budget says so instead of
  reporting a misleading answer time.
- `--max-error-rate` CI gate (`0.05` or `5%`; `0` = no failures allowed):
  exit 3 when too many measured streams failed. `--min-tps`/`--max-ttft`
  only look at successful streams, so a run where 7 of 8 streams were 429'd
  could pass them.

### Changed
- Model comparisons interleave their measured runs (A,B,C, B,C,A, …) after
  all warmups, instead of running every A before any B, so load drift on the
  endpoint — or your network — hits every model equally.
- A later sweep level (or compared model) no longer fails outright because
  one of its warmup streams was rejected (one 429 among eight); it warns and
  measures. The first level/model is still a strict canary.
- API key precedence is now `--api-key` > the provider's own variable for
  the URL's host > `API_KEY` > `OPENAI_API_KEY`. Previously `API_KEY` won
  even when it belonged to a different provider.

### Fixed
- TPS for models that bill hidden reasoning tokens (OpenAI o-series,
  gpt-5): those tokens were generated before the first visible token but
  were divided by the visible generation window, overstating TPS by up to an
  order of magnitude. They are now excluded from the generation rate and
  shown as `thinking … (hidden)`.

## [0.2.0] - 2026-09-13

### Changed
- Generation TPS now uses the standard `(output_tokens - 1) / generation_time`
  definition. The first token is produced during TTFT, so the generation window
  spans N-1 token intervals; the previous formula divided by N and slightly
  overstated throughput (matters most for short outputs). Matches vLLM, NVIDIA
  genai-perf, and Anyscale llmperf.
- The `estimated` token fallback (used when a server omits `usage`) now
  approximates tokens from text length at ~4 chars/token instead of counting SSE
  chunks, which depended on the server's arbitrary chunking.
- Under `--concurrency` / `--sweep`, all streams now share one connection pool
  sized to the concurrency level, so warmup pre-establishes every connection the
  measured runs reuse. Previously the default two-idle-connections-per-host limit
  meant high-concurrency batches paid TCP/TLS setup that warmup is meant to
  absorb, skewing the numbers.
- The text summary no longer drops to the single-shot block when only one of
  several runs survived; it shows the percentile block plus the `errors` line.

### Added
- Warmup + repeated measurement: `--runs` (default 5) timed requests after
  `--warmup` (default 1) discarded ones, reported as median (p50) plus the
  observed min–max range, so a single cold start or network hiccup doesn't skew
  the number. Use `--runs 1 --warmup 0` for a single cheap request.
- `--detail` adds an inter-chunk latency (ITL) line — p50/p95 of the gaps
  between successive content-bearing SSE chunks, pooled across runs, surfacing
  jitter that an averaged rate hides.
- `--json` emits the full result as machine-readable JSON (per-metric
  min/p50/max, ITL, and a `runs_detail` array) for CI gates and diffing.
- `--concurrency N` fires N parallel streams per run and reports aggregate
  tokens/sec under load alongside the per-stream metrics.
- `--sweep=1,2,4,8` (or a bare `--sweep` for the default `1,2,4,8` curve)
  benchmarks across concurrency levels and prints the throughput-vs-concurrency
  curve (text or JSON), now with min–max ranges per level.
- `--max-tokens-field` selects the output-cap request field
  (`max_tokens` or `max_completion_tokens`); tokps also retries automatically
  with `max_completion_tokens` when an endpoint rejects `max_tokens` with a 400.
- `--cost-in` / `--cost-out` (USD per 1M tokens) add a per-request cost line
  (median across runs) and a per-run `cost_usd` in JSON output.
- `--chars-per-token` overrides the ~4 chars/token ratio used by the
  `estimated` fallback (e.g. ~1.5–2 for CJK text).
- Graceful Ctrl-C handling: an interrupted benchmark reports how many measured
  runs completed and exits with code 130 instead of dying silently.
- `--model a,b,c` compare mode: benchmarks each model in turn against the same
  endpoint and prints one row per model (text, `--md`, or a `--json` array).
- Failures under load are recorded instead of aborting: the summary gains an
  `errors` line, `--json` gains `streams`/`errors`/`error_rate`/`errors_detail`,
  and a sweep level or compared model with no successful stream renders as a
  `failed (…)` row. The first level/model stays a fail-fast canary.
- `--min-tps` / `--max-ttft` CI gates: violated thresholds print `FAIL:` lines
  and exit with code 3.
- `--extra-body '{"temperature":0}'` merges arbitrary JSON into the request
  body (your keys win) — provider-specific knobs without new flags.
- `--md` emits GitHub-flavored markdown tables for pasting into issues.
- Live progress on stderr (`run 3/5   72.1 tok/s`) when it is a terminal;
  `--quiet` suppresses progress and warnings.
- Release artifacts now include `checksums.txt`, and a multi-arch image is
  published to `ghcr.io/canergulay/tokps` on every tag.

## [0.1.0] - 2026-06-29

### Added
- Single-shot CLI that measures token-generation throughput (TPS) of any
  OpenAI-compatible `/chat/completions` endpoint.
- Streaming token counting from the `usage` field (`exact`), with a fallback
  that counts streamed chunks (`estimated`).
- Metrics: time-to-first-token (TTFT), generation time, total wall time,
  generation TPS, and end-to-end TPS.
- Support for reasoning models that stream `delta.reasoning_content`
  (e.g. GLM-5.2, DeepSeek-R1) — those tokens count toward timing and throughput.
- Graceful fallback for endpoints that ignore streaming and return a single
  JSON response.
- Provider-agnostic `API_KEY` environment variable, with `OPENAI_API_KEY` as a
  fallback.
- `--version` flag.

[Unreleased]: https://github.com/canergulay/tokps/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/canergulay/tokps/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/canergulay/tokps/releases/tag/v0.1.0
