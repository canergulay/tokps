<!--
Thanks for contributing to tokps. Comments like this one don't render, so keep them or delete them.
Bigger than a bug fix? Open an issue first so we can agree on the approach.
-->

## What and why

<!-- The change in a sentence or two, and the problem it solves.
     For a bug: what tokps did, and what it should have done. -->

Closes #

## Impact

<!-- Keep the lines that apply and delete the rest. tokps exists to report honest numbers,
     so this is the section reviewers read most closely. Renaming or removing a flag or a
     `--json` field breaks someone's CI script: if you do, say so in "What and why". -->

- **None at runtime**: refactor, tests, docs, or CI only
- **Numbers**: how TTFT, TPS, e2e, ITL, token counts, or cost are measured or computed
- **Output**: the text summary, `--json`, or `--md`
- **Interface**: flags, defaults, or exit codes
- **Requests**: what is sent, where, or how many billable calls a run makes
- **API keys**: which key is sent to which host

## Testing

<!-- Unit tests use httptest and never touch the network, so real-endpoint runs are how
     provider quirks get caught. List each endpoint you ran, for example:
       - api.deepseek.com · deepseek-chat · token counts: exact
       - local vLLM · Qwen3-8B · token counts: estimated
     No real run? Say so. That's fine for docs and refactors. -->

Ran against:

-

<details>
<summary>Before / after output</summary>

<!-- Expected for Numbers and Output changes. Run both builds against the same endpoint with
     the same flags, back to back, and redact keys. Run-to-run noise is normal; point out the
     difference your change causes. `--md` output pasted outside a code fence renders as a table. -->

Before:

```text

```

After:

```text

```

</details>

## Checklist

- [ ] Tests cover the change, using `httptest` (no real network)
- [ ] `gofmt -l .` prints nothing; `go vet ./...` and `go test ./...` pass
- [ ] Still zero dependencies (or a new one was agreed in an issue first)
- [ ] README updated for any change to a flag, the output, or behavior
- [ ] `CHANGELOG.md` entry under `[Unreleased]`
