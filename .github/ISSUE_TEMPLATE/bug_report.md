---
name: Bug report
about: Report incorrect behavior, wrong numbers, or a crash
title: ''
labels: bug
assignees: ''
---

**What happened**
A clear description of the bug.

**Command you ran** (redact your API key)
```sh
tokencounter --url ... --model ...
```

**Output you got vs. expected**
```
paste output here
```

**Endpoint details**
- Provider / `--url`:
- `--model`:

**For token-count or timing issues**, a few raw SSE lines help a lot:
```sh
curl -s <url>/chat/completions -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"<model>","messages":[{"role":"user","content":"hi"}],"max_tokens":40,"stream":true}' | head
```

**Environment**
- tokencounter version (`tokencounter --version`):
- Go version (`go version`):
- OS:
