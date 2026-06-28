package bench

import "testing"

func TestEndpointAppendsChatCompletions(t *testing.T) {
	cases := map[string]string{
		"https://api.openai.com/v1":            "https://api.openai.com/v1/chat/completions",
		"https://api.openai.com/v1/":           "https://api.openai.com/v1/chat/completions",
		"https://open.bigmodel.cn/api/paas/v4": "https://open.bigmodel.cn/api/paas/v4/chat/completions",
		"https://x.test/v1/chat/completions":   "https://x.test/v1/chat/completions",
		"https://x.test/v1/chat/completions/":  "https://x.test/v1/chat/completions",
	}
	for in, want := range cases {
		if got := endpoint(in); got != want {
			t.Errorf("endpoint(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHostOf(t *testing.T) {
	if got := hostOf("https://open.bigmodel.cn/api/paas/v4"); got != "open.bigmodel.cn" {
		t.Errorf("hostOf = %q, want open.bigmodel.cn", got)
	}
	if got := hostOf("not a url"); got != "not a url" {
		t.Errorf("hostOf fallback = %q, want raw string", got)
	}
}
