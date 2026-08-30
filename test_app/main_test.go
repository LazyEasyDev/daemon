package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestParseConfig(t *testing.T) {
	args := []string{
		"--enabled=true",
		"--message", "hello service",
		"--count", "7",
		"--port", "18081",
	}
	cfg, err := parseConfig(args)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.Message != "hello service" || cfg.Count != 7 || cfg.Port != 18081 {
		t.Fatalf("parsed config = %+v", cfg)
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

func TestApplicationStartReportsPortConflict(t *testing.T) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	app := newApplication(config{Port: port}, nil, "/opt/test-app")
	if err := app.start(); err == nil {
		t.Fatalf("start() accepted occupied address %s", fmt.Sprintf(":%d", port))
	}
}
