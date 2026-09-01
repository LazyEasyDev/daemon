package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestFormatStopProgress(t *testing.T) {
	tests := []struct {
		name    string
		elapsed time.Duration
		want    string
	}{
		{name: "first tick", elapsed: time.Second, want: "Stopping worker... 1s elapsed"},
		{name: "later tick", elapsed: 44*time.Second + 900*time.Millisecond, want: "Stopping worker... 44s elapsed"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatStopProgress("worker", test.elapsed); got != test.want {
				t.Fatalf("formatStopProgress() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestStopProgressWaitsBeforeDisplaying(t *testing.T) {
	var output bytes.Buffer
	finish := beginStopProgress(&output, "worker", time.Hour)
	finish()
	if output.Len() != 0 {
		t.Fatalf("fast stop progress output = %q, want empty", output.String())
	}
}

func TestStopProgressDisplaysAndClears(t *testing.T) {
	var output bytes.Buffer
	finish := beginStopProgress(&output, "worker", time.Millisecond)
	<-time.After(5 * time.Millisecond)
	finish()

	got := output.String()
	if !strings.Contains(got, "\rStopping worker... 1s elapsed") {
		t.Fatalf("stop progress output = %q, want countdown", got)
	}
	if !strings.HasSuffix(got, "\r") {
		t.Fatalf("stop progress output = %q, want cleared line", got)
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("stop progress output = %q, want no ANSI escapes", got)
	}
}