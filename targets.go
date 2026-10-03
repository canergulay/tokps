package main

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/canergulay/tokps/internal/bench"
)

// providerKeyEnv maps well-known API hosts to the environment variable their
// SDKs read, so `tokps --url https://api.deepseek.com` finds DEEPSEEK_API_KEY
// and a cross-provider compare needs no per-target key flags.
var providerKeyEnv = map[string]string{
	"api.openai.com":                    "OPENAI_API_KEY",
	"api.anthropic.com":                 "ANTHROPIC_API_KEY",
	"api.deepseek.com":                  "DEEPSEEK_API_KEY",
	"generativelanguage.googleapis.com": "GEMINI_API_KEY",
	"api.x.ai":                          "XAI_API_KEY",
	"api.mistral.ai":                    "MISTRAL_API_KEY",
	"api.groq.com":                      "GROQ_API_KEY",
	"api.cerebras.ai":                   "CEREBRAS_API_KEY",
	"api.together.xyz":                  "TOGETHER_API_KEY",
	"api.fireworks.ai":                  "FIREWORKS_API_KEY",
	"openrouter.ai":                     "OPENROUTER_API_KEY",
	"api.deepinfra.com":                 "DEEPINFRA_API_KEY",
	"api.sambanova.ai":                  "SAMBANOVA_API_KEY",
	"api.perplexity.ai":                 "PERPLEXITY_API_KEY",
	"api.moonshot.ai":                   "MOONSHOT_API_KEY",
	"api.z.ai":                          "ZAI_API_KEY",
	"open.bigmodel.cn":                  "ZHIPUAI_API_KEY",
	"dashscope-intl.aliyuncs.com":       "DASHSCOPE_API_KEY",
	"dashscope.aliyuncs.com":            "DASHSCOPE_API_KEY",
}

// providerEnv returns the conventional key variable for rawURL's host, or "".
func providerEnv(rawURL string) string {
	s := rawURL
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	return providerKeyEnv[strings.ToLower(u.Hostname())]
}

// resolveKey picks the API key for one endpoint. Precedence: an explicit
// per-target variable (model@url#VAR) > --api-key > the provider's own
// variable for the URL's host (e.g. DEEPSEEK_API_KEY) > API_KEY >
// OPENAI_API_KEY. The OPENAI_API_KEY fallback is skipped for other known
// providers, so an OpenAI key is never sent to, say, DeepSeek; unknown hosts
// (local servers, gateways) keep it, per the OpenAI-compatible convention.
func resolveKey(explicitEnv, flagKey, rawURL string, getenv func(string) string) (string, error) {
	if explicitEnv != "" {
		if k := getenv(explicitEnv); k != "" {
			return k, nil
		}
		return "", fmt.Errorf("$%s is empty or unset", explicitEnv)
	}
	if flagKey != "" {
		return flagKey, nil
	}
	own := providerEnv(rawURL)
	names := []string{own, "API_KEY"}
	if own == "" || own == "OPENAI_API_KEY" {
		names = append(names, "OPENAI_API_KEY")
	}
	for _, name := range names {
		if name == "" {
			continue
		}
		if k := getenv(name); k != "" {
			return k, nil
		}
	}
	return "", nil
}

// targetSpec is one parsed --target value: model[@url][#ENV_VAR].
type targetSpec struct {
	model, url, keyEnv string
}

// parseTarget parses "model[@url][#ENV_VAR]". The URL starts at the first
// '@' followed by something URL-shaped, so model names that contain '@'
// themselves (Cloudflare's "@cf/meta/…") survive; '#' (a URL fragment,
// meaningless to an API) names the key variable.
func parseTarget(v string) (targetSpec, error) {
	var t targetSpec
	if i := strings.LastIndex(v, "#"); i >= 0 {
		v, t.keyEnv = v[:i], strings.TrimSpace(v[i+1:])
		if t.keyEnv == "" {
			return t, fmt.Errorf("empty key variable after '#'")
		}
	}
	t.model = v
	for i := 0; i < len(v); i++ {
		if v[i] == '@' && looksLikeURL(v[i+1:]) {
			t.model, t.url = v[:i], v[i+1:]
			break
		}
	}
	t.model, t.url = strings.TrimSpace(t.model), strings.TrimSpace(t.url)
	if t.model == "" {
		return t, fmt.Errorf("missing model name in %q", v)
	}
	return t, nil
}

// looksLikeURL reports whether s starts like an endpoint: a scheme, or a
// host that is localhost, carries a port, or ends in an alphabetic TLD.
func looksLikeURL(s string) bool {
	if scheme, _, ok := strings.Cut(s, "://"); ok && isScheme(scheme) {
		return true
	}
	host, _, _ := strings.Cut(s, "/")
	if host == "localhost" || strings.Contains(host, ":") {
		return true
	}
	dot := strings.LastIndex(host, ".")
	if dot < 0 || dot == len(host)-1 {
		return false
	}
	for _, r := range host[dot+1:] {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

// isScheme reports whether s is a URL scheme: a letter followed by letters,
// digits, '+', '-' or '.'.
func isScheme(s string) bool {
	for i, r := range s {
		letter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !letter && (i == 0 || !strings.ContainsRune("0123456789+-.", r)) {
			return false
		}
	}
	return s != ""
}

// targetsValue collects repeated --target flags.
type targetsValue []targetSpec

func (t *targetsValue) String() string {
	parts := make([]string, len(*t))
	for i, s := range *t {
		parts[i] = s.model + "@" + s.url
	}
	return strings.Join(parts, " ")
}

func (t *targetsValue) Set(v string) error {
	spec, err := parseTarget(v)
	if err != nil {
		return err
	}
	*t = append(*t, spec)
	return nil
}

// rateValue parses an error-rate threshold given as a fraction (0.05) or a
// percentage (5%). It stays nil until set, so 0 can mean "no failures".
type rateValue struct{ v *float64 }

func (r *rateValue) String() string {
	if r.v == nil {
		return ""
	}
	return strconv.FormatFloat(*r.v, 'g', -1, 64)
}

func (r *rateValue) Set(s string) error {
	s = strings.TrimSpace(s)
	div := 1.0
	if strings.HasSuffix(s, "%") {
		s, div = strings.TrimSuffix(s, "%"), 100
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("want a fraction like 0.05 or a percentage like 5%%")
	}
	f /= div
	if math.IsNaN(f) || f < 0 || f > 1 {
		return fmt.Errorf("must be between 0 and 1 (or 0%% and 100%%)")
	}
	r.v = &f
	return nil
}

// buildTargets resolves the endpoints to benchmark from --target or --model,
// filling URLs and keys. It returns a usage error for bad combinations.
func buildTargets(opts *options, getenv func(string) string) ([]bench.Target, error) {
	if len(opts.targets) > 0 && opts.model != "" {
		return nil, fmt.Errorf("--target and --model cannot be combined (put the model in each --target)")
	}
	specs := []targetSpec(opts.targets)
	if len(specs) == 0 {
		if opts.model == "" {
			return nil, fmt.Errorf("--url and --model (or --target model@url) are required")
		}
		models, err := bench.ParseModels(opts.model)
		if err != nil {
			return nil, fmt.Errorf("--model: %v", err)
		}
		for _, m := range models {
			specs = append(specs, targetSpec{model: m})
		}
	}
	targets := make([]bench.Target, len(specs))
	for i, s := range specs {
		u := s.url
		if u == "" {
			u = opts.url
		}
		if u == "" {
			return nil, fmt.Errorf("--url is required (or give each --target as model@url)")
		}
		key, err := resolveKey(s.keyEnv, opts.apiKey, u, getenv)
		if err != nil {
			return nil, fmt.Errorf("target %s: %v", s.model, err)
		}
		targets[i] = bench.Target{Model: s.model, URL: u, APIKey: key}
	}
	return targets, nil
}

// missingKeyWarnings names the variable to set for each known-provider target
// that resolved no key — such a run would only collect 401s.
func missingKeyWarnings(targets []bench.Target) []string {
	var out []string
	for _, t := range targets {
		if env := providerEnv(t.URL); t.APIKey == "" && env != "" {
			out = append(out, fmt.Sprintf("no API key for %s (model %s) — set %s or API_KEY", t.URL, t.Model, env))
		}
	}
	return out
}
