package main

import (
	"bytes"
	"strings"
	"testing"
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
