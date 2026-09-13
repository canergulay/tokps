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

	sums, err := RunCompare(context.Background(), cfg, []string{"fast", "missing", "slow"}, 1, 0, 1)
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

	_, err := RunCompare(context.Background(), testConfig(ts.URL), []string{"missing", "fast"}, 1, 0, 1)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("error = %v, want a 404 abort on the canary model", err)
	}
}
