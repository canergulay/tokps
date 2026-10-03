package main

import (
	"fmt"
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
// OPENAI_API_KEY.
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
	for _, name := range []string{providerEnv(rawURL), "API_KEY", "OPENAI_API_KEY"} {
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

// parseTarget parses "model[@url][#ENV_VAR]". The model ends at the first
// '@'; '#' (a URL fragment, meaningless to an API) names the key variable.
func parseTarget(v string) (targetSpec, error) {
	var t targetSpec
	if i := strings.LastIndex(v, "#"); i >= 0 {
		v, t.keyEnv = v[:i], strings.TrimSpace(v[i+1:])
		if t.keyEnv == "" {
			return t, fmt.Errorf("empty key variable after '#'")
		}
	}
	t.model, t.url, _ = strings.Cut(v, "@")
	t.model, t.url = strings.TrimSpace(t.model), strings.TrimSpace(t.url)
	if t.model == "" {
		return t, fmt.Errorf("missing model name in %q", v)
	}
	return t, nil
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
	if f < 0 || f > 1 {
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
