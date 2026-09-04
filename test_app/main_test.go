package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestParseConfig(t *testing.T) {
	args := []string{
		"--enabled=true",
		"--message", "hello service",
		"--count", "7",
		"--port", "18081",
		"--file-path", "relative-path-test.txt",
		"--stop-after", "30s",
		"--stop_delay", "45s",
		"--event-path", "events.jsonl",
		"--spawn-child=true",
		"--child-pid-path", "child.pid",
	}
	cfg, err := parseConfig(args)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.Message != "hello service" || cfg.Count != 7 || cfg.Port != 18081 || cfg.FilePath != "relative-path-test.txt" || cfg.StopAfter != 30*time.Second || cfg.StopDelay != 45*time.Second || cfg.EventPath != "events.jsonl" || !cfg.SpawnChild || cfg.ChildPIDPath != "child.pid" {
		t.Fatalf("parsed config = %+v", cfg)
	}
}

func TestRecordLifecycleEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	app := newApplication(config{Port: 18080, EventPath: path}, nil, "/opt/test-app", "")
	if err := app.recordEvent("started"); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var event lifecycleEvent
	if err := json.Unmarshal(content, &event); err != nil {
		t.Fatal(err)
	}
	if event.Event != "started" || event.PID != os.Getpid() || event.Time.IsZero() {
		t.Fatalf("event = %+v", event)
	}
}

func TestReadConfiguredFileRelativeToWorkingDirectory(t *testing.T) {
	directory := t.TempDir()
	const filename = "relative-path-test.txt"
	const want = "relative path works\n"
	if err := os.WriteFile(filepath.Join(directory, filename), []byte(want), 0644); err != nil {
		t.Fatal(err)
	}
	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousDirectory) })

	got, err := readConfiguredFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("file content = %q, want %q", got, want)
	}
}

func TestParseConfigRejectsNegativeStopDelay(t *testing.T) {
	if _, err := parseConfig([]string{"--stop_delay", "-1s"}); err == nil {
		t.Fatal("parseConfig() accepted a negative stop delay")
	}
}

func TestStatusHandlerReturnsConfigAndArgs(t *testing.T) {
	args := []string{"--enabled=true", "--message", "hello service", "--count", "7"}
	cfg := config{Enabled: true, Message: "hello service", Count: 7, Port: 18080}
	app := newApplication(cfg, args, "/opt/test-app", "relative path works\n")
	request := httptest.NewRequest("GET", "/", nil)
	recorder := httptest.NewRecorder()

	app.handleStatus(recorder, request)

	if recorder.Code != 200 {
		t.Fatalf("status code = %d, want 200", recorder.Code)
	}
	var response status
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Config != cfg {
		t.Fatalf("response config = %+v, want %+v", response.Config, cfg)
	}
	if !slices.Equal(response.Args, args) {
		t.Fatalf("response args = %q, want %q", response.Args, args)
	}
	if response.FileContent != "relative path works\n" {
		t.Fatalf("file content = %q, want fixture content", response.FileContent)
	}
	if response.CurrentTime.IsZero() || response.StartedAt.IsZero() {
		t.Fatal("response timestamps must be populated")
	}
}

func TestParseConfigRejectsInvalidPort(t *testing.T) {
	if _, err := parseConfig([]string{"--port", "70000"}); err == nil {
		t.Fatal("parseConfig() accepted an invalid port")
	}
}

func TestApplicationStartCanRetryAfterPortConflict(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	app := newApplication(config{Port: port}, nil, "/opt/test-app", "")
	if err := app.start(); err == nil {
		t.Fatalf("start() accepted occupied address %s", fmt.Sprintf("127.0.0.1:%d", port))
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := app.start(); err != nil {
		t.Fatalf("start() did not recover after the address was released: %v", err)
	}
	app.Stop()
}

func TestApplicationStopsAfterConfiguredDuration(t *testing.T) {
	app := newApplication(config{Port: 0, StopAfter: 10 * time.Millisecond}, nil, "/opt/test-app", "")
	if err := app.run(); !errors.Is(err, errStopAfter) {
		t.Fatalf("run() error = %v, want %v", err, errStopAfter)
	}
}

func TestApplicationDelaysGracefulStop(t *testing.T) {
	const stopDelay = 50 * time.Millisecond
	app := newApplication(config{Port: 0, StopDelay: stopDelay}, nil, "/opt/test-app", "")
	if err := app.start(); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	app.Stop()
	if elapsed := time.Since(started); elapsed < stopDelay {
		t.Fatalf("Stop() returned after %v, want at least %v", elapsed, stopDelay)
	}
}
