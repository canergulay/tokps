package sse

import (
	"reflect"
	"strings"
	"testing"
)

func TestScannerYieldsDataPayloads(t *testing.T) {
	input := "data: {\"a\":1}\n\ndata: {\"b\":2}\n\ndata: [DONE]\n\n"
	sc := NewScanner(strings.NewReader(input))

	var got []string
	for sc.Scan() {
		got = append(got, sc.Data())
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{`{"a":1}`, `{"b":2}`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestScannerSkipsNonDataAndBlankLines(t *testing.T) {
	input := ": comment\nevent: message\n\ndata: {\"x\":1}\n\ngarbage line\n\ndata: {\"y\":2}\n\n"
	sc := NewScanner(strings.NewReader(input))

	var got []string
	for sc.Scan() {
		got = append(got, sc.Data())
	}

	want := []string{`{"x":1}`, `{"y":2}`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestScannerStopsAtDone(t *testing.T) {
	input := "data: {\"a\":1}\n\ndata: [DONE]\n\ndata: {\"never\":1}\n\n"
	sc := NewScanner(strings.NewReader(input))

	var got []string
	for sc.Scan() {
		got = append(got, sc.Data())
	}

	want := []string{`{"a":1}`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
