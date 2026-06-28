# Contributing to tokps

Thanks for your interest in improving tokps! Contributions of all kinds
are welcome — bug reports, feature ideas, and pull requests.

## Getting started

```sh
git clone https://github.com/canergulay/tokps
cd tokps
go build ./...
go test ./...
```

You need Go 1.23 or newer. The project uses only the Go standard library;
please keep it dependency-free unless there's a strong reason to add one
(open an issue first to discuss).

## Development workflow

1. Open an issue describing the bug or feature before large changes, so we can
   agree on the approach.
2. Write a failing test that captures the behavior you're changing, then make
   it pass. Every package under `internal/` is tested with `httptest` — no
   real network access is required.
3. Keep changes focused. One logical change per pull request.

Before pushing:

```sh
gofmt -l .        # should print nothing
go vet ./...
go test ./...
```

## Code style

- Run `gofmt` (or `go fmt ./...`) — formatting is not negotiable.
- Match the surrounding code: small focused files, exported identifiers
  documented with a comment starting with the identifier's name.
- Prefer the standard library.

## Reporting bugs

Open an issue with:

- the exact command you ran (redact your API key),
- the `--url` and `--model`,
- the output you got and what you expected,
- your Go version (`go version`) and OS.

If the issue is about token counts or timing, a few raw SSE lines from the
endpoint (with `curl`) are extremely helpful.

## Pull requests

- Reference the issue it addresses.
- Include tests for new behavior.
- Make sure CI-equivalent checks (`gofmt`, `go vet`, `go test ./...`) pass
  locally.

By contributing, you agree that your contributions are licensed under the
project's [MIT License](LICENSE).
