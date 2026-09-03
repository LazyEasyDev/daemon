package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestConfirmInstallWarning(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		ignoreWarnings bool
		interactive    bool
		wantErr        string
		wantOutput     string
	}{
		{name: "yes", input: "yes\n", interactive: true, wantOutput: "Warning: risky deployment\nContinue installation? [y/N] "},
		{name: "short yes", input: "Y\n", interactive: true, wantOutput: "Warning: risky deployment\nContinue installation? [y/N] "},
		{name: "no", input: "no\n", interactive: true, wantErr: "installation cancelled"},
		{name: "default no", input: "\n", interactive: true, wantErr: "installation cancelled"},
		{name: "end of input", interactive: true, wantErr: "installation cancelled"},
		{name: "noninteractive", wantErr: "requires a terminal"},
		{name: "ignored", ignoreWarnings: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			err := confirmInstallWarning(strings.NewReader(test.input), &output, "Warning: risky deployment", test.ignoreWarnings, test.interactive)
			if test.wantErr == "" && err != nil {
				t.Fatalf("confirmInstallWarning() error = %v", err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("confirmInstallWarning() error = %v, want error containing %q", err, test.wantErr)
			}
			if test.wantOutput != "" && output.String() != test.wantOutput {
				t.Fatalf("output = %q, want %q", output.String(), test.wantOutput)
			}
			if test.ignoreWarnings && output.Len() != 0 {
				t.Fatalf("ignored warning output = %q, want empty", output.String())
			}
		})
	}
}

func TestConfirmInstallWarningFailuresDoNotBlockInstallation(t *testing.T) {
	if err := confirmInstallWarning(strings.NewReader("yes\n"), failingWriter{}, "Warning: risky deployment", false, true); err != nil {
		t.Fatalf("warning output failure blocked installation: %v", err)
	}

	var output bytes.Buffer
	if err := confirmInstallWarning(failingReader{}, &output, "Warning: risky deployment", false, true); err != nil {
		t.Fatalf("warning input failure blocked installation: %v", err)
	}
}

type notifyingBuffer struct {
	bytes.Buffer
	written chan struct{}
}

func (buffer *notifyingBuffer) Write(data []byte) (int, error) {
	written, err := buffer.Buffer.Write(data)
	select {
	case buffer.written <- struct{}{}:
	default:
	}
	return written, err
}

func TestFormatStopProgress(t *testing.T) {
	tests := []struct {
		name    string
		elapsed time.Duration
		wait    time.Duration
		want    string
	}{
		{name: "first tick", elapsed: time.Second, want: "Stopping worker... 1s elapsed"},
		{name: "later tick", elapsed: 44*time.Second + 900*time.Millisecond, want: "Stopping worker... 44s elapsed"},
		{name: "time limit", elapsed: 15 * time.Second, wait: 45 * time.Second, want: "Stopping worker... 15s elapsed (within time limit: 45s)"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatStopProgress("worker", test.elapsed, test.wait); got != test.want {
				t.Fatalf("formatStopProgress() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestStopProgressWaitsBeforeDisplaying(t *testing.T) {
	var output bytes.Buffer
	finish := beginStopProgress(&output, "worker", 45*time.Second, time.Hour)
	finish()
	if output.Len() != 0 {
		t.Fatalf("fast stop progress output = %q, want empty", output.String())
	}
}

func TestStopProgressDisplaysAndClears(t *testing.T) {
	output := notifyingBuffer{written: make(chan struct{}, 1)}
	finish := beginStopProgress(&output, "worker", 45*time.Second, time.Millisecond)
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	select {
	case <-output.written:
	case <-timeout.C:
		finish()
		t.Fatal("timed out waiting for stop progress output")
	}
	finish()

	got := output.String()
	if !strings.Contains(got, "\rStopping worker... 1s elapsed (within time limit: 45s)") {
		t.Fatalf("stop progress output = %q, want countdown", got)
	}
	if !strings.HasSuffix(got, "\r") {
		t.Fatalf("stop progress output = %q, want cleared line", got)
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("stop progress output = %q, want no ANSI escapes", got)
	}
}
