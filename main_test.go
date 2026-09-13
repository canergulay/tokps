package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/canergulay/tokps/internal/bench"
)

func TestRunVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--version"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.HasPrefix(out.String(), "tokps ") {
		t.Errorf("stdout = %q, want a version line", out.String())
	}
}

func TestRunRequiresURLAndModel(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--url and --model are required") {
		t.Errorf("stderr = %q, want the required-flags error", errb.String())
	}
}

func TestRunRejectsBadSweepLevels(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--url=http://x", "--model=m", "--sweep=1,x,3"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestRunRejectsBadMaxTokensField(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--url=http://x", "--model=m", "--max-tokens-field=foo"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "max-tokens-field") {
		t.Errorf("stderr = %q, want it to mention --max-tokens-field", errb.String())
	}
}

func TestRunHintsAtSweepEqualsSyntax(t *testing.T) {
	// A bare --sweep does not consume the next arg (bool-flag semantics), so
	// "1,2,4" lands in the positional args. We hint at the = syntax instead of
	// silently running the default curve.
	var out, errb bytes.Buffer
	code := run([]string{"--url=http://x", "--model=m", "--sweep", "1,2,4"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--sweep=1,2,4") {
		t.Errorf("stderr = %q, want a --sweep=1,2,4 hint", errb.String())
	}
}

func TestRunRejectsNegativeCost(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--url=http://x", "--model=m", "--cost-in=-1"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "must be >= 0") {
		t.Errorf("stderr = %q, want the cost validation error", errb.String())
	}
}

func TestRunRejectsBadCharsPerToken(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--url=http://x", "--model=m", "--chars-per-token=0"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--chars-per-token") {
		t.Errorf("stderr = %q, want it to mention --chars-per-token", errb.String())
	}
}

func TestRunRejectsNonPositiveTimeout(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--url=http://x", "--model=m", "--timeout=0s"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--timeout") {
		t.Errorf("stderr = %q, want it to mention --timeout", errb.String())
	}
}

func TestSweepValueDefaults(t *testing.T) {
	var sv sweepValue
	if err := sv.Set("true"); err != nil {
		t.Fatalf("Set(\"true\") error: %v", err)
	}
	if !sv.enabled {
		t.Error("enabled = false, want true")
	}
	want := []int{1, 2, 4, 8}
	if len(sv.levels) != len(want) {
		t.Fatalf("levels = %v, want %v", sv.levels, want)
	}
	for i := range want {
		if sv.levels[i] != want[i] {
			t.Errorf("levels[%d] = %d, want %d", i, sv.levels[i], want[i])
		}
	}
}

func TestSweepValueParsesAndDisables(t *testing.T) {
	var sv sweepValue
	if err := sv.Set("1,2,8"); err != nil {
		t.Fatalf("Set(\"1,2,8\") error: %v", err)
	}
	if !sv.enabled || sv.String() != "1,2,8" {
		t.Errorf("got enabled=%v String=%q, want true/\"1,2,8\"", sv.enabled, sv.String())
	}
	if err := sv.Set("false"); err != nil {
		t.Fatalf("Set(\"false\") error: %v", err)
	}
	if sv.enabled {
		t.Error("enabled = true, want false after Set(\"false\")")
	}
	if sv.String() != "" {
		t.Errorf("String() = %q, want \"\" when disabled", sv.String())
	}
}

func TestRunRejectsNonObjectExtraBody(t *testing.T) {
	for _, bad := range []string{`[1,2]`, `not json`, `null`, `5`} {
		var out, errb bytes.Buffer
		code := run([]string{"--url=http://x", "--model=m", "--extra-body=" + bad}, &out, &errb)
		if code != 2 {
			t.Errorf("--extra-body=%s: exit = %d, want 2", bad, code)
		}
		if !strings.Contains(errb.String(), "--extra-body") {
			t.Errorf("--extra-body=%s: stderr = %q, want it to mention --extra-body", bad, errb.String())
		}
	}
}

// fakeServer serves a minimal streaming completion for end-to-end run() tests.
func fakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{}}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func TestProgressLineFormats(t *testing.T) {
	cases := []struct {
		ev   bench.ProgressEvent
		want string
	}{
		{bench.ProgressEvent{Phase: "warmup", Index: 1, Total: 1, Concurrency: 1}, "warmup 1/1"},
		{bench.ProgressEvent{Phase: "run", Index: 3, Total: 5, Concurrency: 1, BatchTPS: 72.14}, "run 3/5   72.1 tok/s"},
		{bench.ProgressEvent{Phase: "run", Index: 1, Total: 2, Concurrency: 4, BatchTPS: 250, Label: "c=4"}, "c=4  run 1/2   250.0 tok/s (aggregate, 4 streams)"},
		{bench.ProgressEvent{Phase: "run", Index: 1, Total: 2, Concurrency: 4, BatchTPS: 180, Failed: 2}, "run 1/2   180.0 tok/s (aggregate, 4 streams)   2 failed"},
	}
	for _, c := range cases {
		if got := progressLine(c.ev); got != c.want {
			t.Errorf("progressLine(%+v) = %q, want %q", c.ev, got, c.want)
		}
	}
}

func TestIsTerminalFalseForBuffer(t *testing.T) {
	if isTerminal(&bytes.Buffer{}) {
		t.Error("isTerminal(buffer) = true, want false")
	}
}

func TestRunNoProgressWhenStderrNotTerminal(t *testing.T) {
	ts := fakeServer(t)
	defer ts.Close()
	var out, errb bytes.Buffer
	if code := run([]string{"--url=" + ts.URL, "--model=m", "--runs=2", "--warmup=0"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
	}
	if strings.Contains(errb.String(), "run 1/2") {
		t.Errorf("stderr should carry no progress when it is not a terminal:\n%s", errb.String())
	}
	if !strings.Contains(out.String(), "TPS") {
		t.Errorf("stdout missing the summary:\n%s", out.String())
	}
}

func TestRunQuietSuppressesWarnings(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: not json\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{}}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer ts.Close()

	var out, errb bytes.Buffer
	args := []string{"--url=" + ts.URL, "--model=m", "--runs=1", "--warmup=0"}
	if code := run(args, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(errb.String(), "warning:") {
		t.Errorf("without --quiet, stderr should carry the warning:\n%s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := run(append(args, "--quiet"), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.Contains(errb.String(), "warning:") {
		t.Errorf("--quiet should suppress warnings:\n%s", errb.String())
	}
}

func TestRunGateFailureExits3(t *testing.T) {
	ts := fakeServer(t)
	defer ts.Close()
	var out, errb bytes.Buffer
	// The fake server is fast but finite; an absurd threshold must fail.
	code := run([]string{"--url=" + ts.URL, "--model=m", "--runs=2", "--warmup=0", "--min-tps=1e12"}, &out, &errb)
	if code != 3 {
		t.Fatalf("exit = %d, want 3; stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "FAIL: TPS p50") {
		t.Errorf("stderr = %q, want a FAIL line", errb.String())
	}
	if !strings.Contains(out.String(), "TPS") {
		t.Errorf("stdout should still carry the report:\n%s", out.String())
	}
}

func TestRunGatePassExits0(t *testing.T) {
	ts := fakeServer(t)
	defer ts.Close()
	var out, errb bytes.Buffer
	code := run([]string{"--url=" + ts.URL, "--model=m", "--runs=2", "--warmup=0", "--min-tps=0.001", "--max-ttft=1h"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
	}
	if strings.Contains(errb.String(), "FAIL") {
		t.Errorf("stderr should have no FAIL lines:\n%s", errb.String())
	}
}

func TestRunSweepGateLabelsLevel(t *testing.T) {
	ts := fakeServer(t)
	defer ts.Close()
	var out, errb bytes.Buffer
	code := run([]string{"--url=" + ts.URL, "--model=m", "--runs=1", "--warmup=0", "--sweep=1,2", "--min-tps=1e12"}, &out, &errb)
	if code != 3 {
		t.Fatalf("exit = %d, want 3; stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "FAIL: c=1 TPS p50") || !strings.Contains(errb.String(), "FAIL: c=2 TPS p50") {
		t.Errorf("stderr = %q, want per-level FAIL lines", errb.String())
	}
}

func TestRunRejectsNegativeGate(t *testing.T) {
	for _, flag := range []string{"--min-tps=-1", "--max-ttft=-1s"} {
		var out, errb bytes.Buffer
		if code := run([]string{"--url=http://x", "--model=m", flag}, &out, &errb); code != 2 {
			t.Errorf("%s: exit = %d, want 2", flag, code)
		}
		if !strings.Contains(errb.String(), "--min-tps") && !strings.Contains(errb.String(), "--max-ttft") {
			t.Errorf("%s: stderr = %q, want the gate validation error", flag, errb.String())
		}
	}
}

func TestRunRejectsCompareWithSweep(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--url=http://x", "--model=a,b", "--sweep"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--sweep") || !strings.Contains(errb.String(), "--model") {
		t.Errorf("stderr = %q, want the sweep/compare conflict error", errb.String())
	}
}

func TestRunRejectsEmptyModelEntry(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--url=http://x", "--model=a,,b"}, &out, &errb); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--model") {
		t.Errorf("stderr = %q, want it to mention --model", errb.String())
	}
}

func TestRunCompareEndToEnd(t *testing.T) {
	ts := fakeServer(t)
	defer ts.Close()
	var out, errb bytes.Buffer
	code := run([]string{"--url=" + ts.URL, "--model=alpha, beta", "--runs=1", "--warmup=0"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
	}
	for _, want := range []string{"compare @", "  alpha ", "  beta "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout missing %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	errb.Reset()
	code = run([]string{"--url=" + ts.URL, "--model=alpha,beta", "--runs=1", "--warmup=0", "--json"}, &out, &errb)
	if code != 0 || !strings.HasPrefix(strings.TrimSpace(out.String()), "[") {
		t.Errorf("--json compare: exit=%d stdout=%q, want a JSON array", code, out.String())
	}

	out.Reset()
	errb.Reset()
	code = run([]string{"--url=" + ts.URL, "--model=alpha,beta", "--runs=1", "--warmup=0", "--min-tps=1e12"}, &out, &errb)
	if code != 3 || !strings.Contains(errb.String(), "FAIL: alpha TPS p50") || !strings.Contains(errb.String(), "FAIL: beta TPS p50") {
		t.Errorf("compare gates: exit=%d stderr=%q, want exit 3 with per-model FAIL lines", code, errb.String())
	}
}

func TestRunRejectsMdWithJSON(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--url=http://x", "--model=m", "--md", "--json"}, &out, &errb); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--md") || !strings.Contains(errb.String(), "--json") {
		t.Errorf("stderr = %q, want the --md/--json conflict error", errb.String())
	}
}

func TestRunMdEndToEnd(t *testing.T) {
	ts := fakeServer(t)
	defer ts.Close()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"summary", []string{"--runs=2", "--warmup=0", "--md"}, "| metric | p50 | min | max |"},
		{"single", []string{"--runs=1", "--warmup=0", "--md"}, "| metric | value |"},
		{"sweep", []string{"--runs=1", "--warmup=0", "--sweep=1,2", "--md"}, "| concurrency |"},
		{"compare", []string{"--runs=1", "--warmup=0", "--model=a,b", "--md"}, "| model |"},
	}
	for _, c := range cases {
		var out, errb bytes.Buffer
		args := append([]string{"--url=" + ts.URL, "--model=m"}, c.args...)
		if code := run(args, &out, &errb); code != 0 {
			t.Errorf("%s: exit = %d, want 0; stderr=%s", c.name, code, errb.String())
			continue
		}
		if !strings.HasPrefix(out.String(), "**tokps") || !strings.Contains(out.String(), c.want) {
			t.Errorf("%s: stdout = %q, want markdown containing %q", c.name, out.String(), c.want)
		}
	}
}
