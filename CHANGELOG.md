# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-06-28

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

[Unreleased]: https://github.com/canergulay/tokencounter/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/canergulay/tokencounter/releases/tag/v0.1.0
