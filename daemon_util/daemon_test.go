package daemon_util

import (
	"errors"
	"testing"
	"time"
)

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
