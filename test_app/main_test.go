package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
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
		"--stop-after", "30s",
		"--stop_delay", "45s",
	}
	cfg, err := parseConfig(args)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.Message != "hello service" || cfg.Count != 7 || cfg.Port != 18081 || cfg.StopAfter != 30*time.Second || cfg.StopDelay != 45*time.Second {
		t.Fatalf("parsed config = %+v", cfg)
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
	app := newApplication(cfg, args, "/opt/test-app")
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
	app := newApplication(config{Port: port}, nil, "/opt/test-app")
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
	app := newApplication(config{Port: 0, StopAfter: 10 * time.Millisecond}, nil, "/opt/test-app")
	if err := app.run(); !errors.Is(err, errStopAfter) {
		t.Fatalf("run() error = %v, want %v", err, errStopAfter)
	}
}

func TestApplicationStartStopsAfterConfiguredDuration(t *testing.T) {
	app := newApplication(config{Port: 0, StopAfter: 10 * time.Millisecond}, nil, "/opt/test-app")
	fatalErr := make(chan error, 1)
	app.fatal = func(err error) {
		fatalErr <- err
	}
	app.Start()
	defer app.Stop()

	select {
	case err := <-fatalErr:
		if !errors.Is(err, errStopAfter) {
			t.Fatalf("fatal error = %v, want %v", err, errStopAfter)
		}
	case <-time.After(time.Second):
		t.Fatal("Start() did not apply the configured stop-after duration")
	}
}

func TestApplicationDelaysGracefulStop(t *testing.T) {
	const stopDelay = 50 * time.Millisecond
	app := newApplication(config{Port: 0, StopDelay: stopDelay}, nil, "/opt/test-app")
	if err := app.start(); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	app.Stop()
	if elapsed := time.Since(started); elapsed < stopDelay {
		t.Fatalf("Stop() returned after %v, want at least %v", elapsed, stopDelay)
	}
}
