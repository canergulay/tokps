package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestParseModels(t *testing.T) {
	got, err := ParseModels(" a , b,c")
	if err != nil {
		t.Fatalf("ParseModels error: %v", err)
	}
	if strings.Join(got, "|") != "a|b|c" {
		t.Errorf("got %v, want [a b c]", got)
	}
	if one, _ := ParseModels("solo"); len(one) != 1 || one[0] != "solo" {
		t.Errorf("single model = %v, want [solo]", one)
	}
	for _, bad := range []string{"a,,b", "a,", ""} {
		if _, err := ParseModels(bad); err == nil {
			t.Errorf("ParseModels(%q): expected error on empty entry", bad)
		}
	}
}

// modelServer answers per model name: "missing" is 404'd, anything else
// streams a normal completion. It records the models it saw, in order.
func modelServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var cr chatRequest
		_ = json.NewDecoder(r.Body).Decode(&cr)
		mu.Lock()
		seen = append(seen, cr.Model)
		mu.Unlock()
		if cr.Model == "missing" {
			http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{}}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	return ts, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), seen...) }
}

func TestRunCompareRunsEachModelInOrder(t *testing.T) {
	ts, seen := modelServer(t)
	defer ts.Close()

	cfg := testConfig(ts.URL)
	var labels []string
	cfg.Progress = func(ev ProgressEvent) { labels = append(labels, ev.Label) }

	sums, err := RunCompare(context.Background(), cfg, ModelTargets([]string{"fast", "missing", "slow"}), 1, 0, 1)
	if err != nil {
		t.Fatalf("RunCompare error: %v, want nil (a non-canary failure is a row)", err)
	}
	if len(sums) != 3 {
		t.Fatalf("summaries = %d, want 3", len(sums))
	}
	if sums[0].Model != "fast" || sums[1].Model != "missing" || sums[2].Model != "slow" {
		t.Errorf("models = %s,%s,%s want fast,missing,slow", sums[0].Model, sums[1].Model, sums[2].Model)
	}
	if !sums[1].AllFailed() || len(sums[1].Errors) == 0 || sums[1].Errors[0].Status != 404 {
		t.Errorf("missing model should be a failed row with a 404: %+v", sums[1])
	}
	if sums[0].AllFailed() || sums[2].AllFailed() {
		t.Error("fast and slow should have results")
	}
	if got := strings.Join(seen(), ","); got != "fast,missing,slow" {
		t.Errorf("request order = %s, want fast,missing,slow", got)
	}
	if got := strings.Join(labels, ","); got != "fast,missing,slow" {
		t.Errorf("progress labels = %s, want fast,missing,slow", got)
	}
}

func TestRunCompareFirstModelFailureAborts(t *testing.T) {
	ts, _ := modelServer(t)
	defer ts.Close()

	_, err := RunCompare(context.Background(), testConfig(ts.URL), ModelTargets([]string{"missing", "fast"}), 1, 0, 1)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("error = %v, want a 404 abort on the canary model", err)
	}
}

// targetServer streams a normal completion and records the model and
// Authorization header of every request, in order.
func targetServer(t *testing.T, log *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var cr chatRequest
		_ = json.NewDecoder(r.Body).Decode(&cr)
		mu.Lock()
		*log = append(*log, cr.Model+" "+r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{}}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func TestRunCompareInterleavesMeasuredRuns(t *testing.T) {
	ts, seen := modelServer(t)
	defer ts.Close()

	// Warmups first, then rounds that rotate the starting target, so drift
	// over time lands on every target equally.
	_, err := RunCompare(context.Background(), testConfig(ts.URL), ModelTargets([]string{"a", "b", "c"}), 3, 1, 1)
	if err != nil {
		t.Fatalf("RunCompare error: %v", err)
	}
	want := "a,b,c, a,b,c, b,c,a, c,a,b"
	if got := strings.Join(seen(), ","); got != strings.ReplaceAll(want, " ", "") {
		t.Errorf("request order = %s, want %s", got, want)
	}
}

func TestRunCompareTargetsUseTheirOwnURLAndKey(t *testing.T) {
	var mu sync.Mutex
	var logA, logB []string
	a := targetServer(t, &logA, &mu)
	defer a.Close()
	b := targetServer(t, &logB, &mu)
	defer b.Close()

	cfg := testConfig("")
	cfg.APIKey = "shared"
	var labels []string
	cfg.Progress = func(ev ProgressEvent) { labels = append(labels, ev.Label) }
	targets := []Target{
		{Model: "m1", URL: a.URL, APIKey: "key-a"},
		{Model: "m1", URL: b.URL}, // no key of its own: falls back to cfg.APIKey
	}
	sums, err := RunCompare(context.Background(), cfg, targets, 1, 0, 1)
	if err != nil {
		t.Fatalf("RunCompare error: %v", err)
	}
	if strings.Join(logA, ",") != "m1 Bearer key-a" || strings.Join(logB, ",") != "m1 Bearer shared" {
		t.Errorf("requests: a=%v b=%v", logA, logB)
	}
	if sums[0].Host == sums[1].Host {
		t.Errorf("summaries should carry each target's host, both are %s", sums[0].Host)
	}
	want := []string{"m1@" + hostOf(a.URL), "m1@" + hostOf(b.URL)}
	if strings.Join(labels, ",") != strings.Join(want, ",") {
		t.Errorf("labels = %v, want %v (model@host when hosts differ)", labels, want)
	}
}

func TestRunCompareLaterTargetWarmupFailureIsRow(t *testing.T) {
	ts, seen := modelServer(t)
	defer ts.Close()

	sums, err := RunCompare(context.Background(), testConfig(ts.URL), ModelTargets([]string{"fast", "missing"}), 2, 1, 1)
	if err != nil {
		t.Fatalf("RunCompare error: %v, want nil", err)
	}
	if !sums[1].AllFailed() || sums[1].Errors[0].Status != 404 {
		t.Errorf("missing should be a failed row with its warmup 404: %+v", sums[1])
	}
	// The failed target is skipped in the measured rounds.
	if got := strings.Join(seen(), ","); got != "fast,missing,fast,fast" {
		t.Errorf("request order = %s, want fast,missing,fast,fast", got)
	}
}
