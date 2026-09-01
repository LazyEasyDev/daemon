package daemon_util

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type failingExecutable struct {
	err error
}

func (*failingExecutable) Start() error { return nil }

func (*failingExecutable) Stop() error { return nil }

func (executable *failingExecutable) Run() error { return executable.err }

func TestManagedServiceName(t *testing.T) {
	name, err := ManagedServiceName("Worker1")
	if err != nil {
		t.Fatal(err)
	}
	if name != "lz_lz_Worker1" {
		t.Fatalf("managed service name = %q, want %q", name, "lz_lz_Worker1")
	}
}

func TestManagedServiceNameRejectsNonPortableNames(t *testing.T) {
	tests := []string{
		"",
		"1worker",
		"my service",
		"my-service",
		"my_service",
		"my.service",
		"worker@1",
		"wörker",
		strings.Repeat("a", maxServiceNameLength+1),
	}

	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ManagedServiceName(name); !errors.Is(err, ErrInvalidName) {
				t.Fatalf("ManagedServiceName(%q) error = %v, want ErrInvalidName", name, err)
			}
		})
	}
}

func TestValidateServiceNameAcceptsInternalPrefix(t *testing.T) {
	if err := validateServiceName("lz_lz_Worker1"); err != nil {
		t.Fatalf("validateServiceName() error = %v", err)
	}
}

func TestValidateServiceNameRejectsUnsafeRCName(t *testing.T) {
	for _, name := range []string{"1worker", "worker-name", "worker.name", "worker@1"} {
		if err := validateServiceName(name); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("validateServiceName(%q) error = %v, want ErrInvalidName", name, err)
		}
	}
}

func TestServiceConfigStopTimeout(t *testing.T) {
	var config serviceConfig
	if got := config.stopTimeoutDuration(); got != 600*time.Second {
		t.Fatalf("default stop timeout = %v, want 600s", got)
	}

	if err := config.SetStopTimeout(45 * time.Second); err != nil {
		t.Fatal(err)
	}
	if got := config.stopTimeoutSeconds(); got != 45 {
		t.Fatalf("stop timeout seconds = %d, want 45", got)
	}
}

func TestServiceConfigRejectsInvalidStopTimeout(t *testing.T) {
	for _, timeout := range []time.Duration{0, -time.Second, 1500 * time.Millisecond} {
		if err := new(serviceConfig).SetStopTimeout(timeout); !errors.Is(err, ErrInvalidStopTimeout) {
			t.Errorf("SetStopTimeout(%v) error = %v, want %v", timeout, err, ErrInvalidStopTimeout)
		}
	}
}

func TestRunExecutablePropagatesError(t *testing.T) {
	runErr := errors.New("run failed")
	result, err := runExecutable("worker", &failingExecutable{err: runErr})
	if !errors.Is(err, runErr) {
		t.Fatalf("runExecutable() error = %v, want %v", err, runErr)
	}
	if result != "Running worker: [FAILED]" {
		t.Fatalf("runExecutable() result = %q, want %q", result, "Running worker: [FAILED]")
	}
}
