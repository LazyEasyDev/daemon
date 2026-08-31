//go:build windows

package daemon_util

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	winapi "golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

type fakeWindowsService struct {
	statuses     []svc.Status
	controlCalls int
	controlErr   error
}

func (service *fakeWindowsService) Query() (svc.Status, error) {
	status := service.statuses[0]
	if len(service.statuses) > 1 {
		service.statuses = service.statuses[1:]
	}
	return status, nil
}

func (service *fakeWindowsService) Control(svc.Cmd) (svc.Status, error) {
	service.controlCalls++
	return svc.Status{}, service.controlErr
}

func TestWaitForServiceStateReturnsRunning(t *testing.T) {
	service := &fakeWindowsService{statuses: []svc.Status{{State: svc.Running}}}
	status, err := waitForServiceState(service, svc.Running, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != svc.Running {
		t.Fatalf("state = %v, want %v", status.State, svc.Running)
	}
}

func TestWaitForServiceStateReturnsStartFailure(t *testing.T) {
	service := &fakeWindowsService{statuses: []svc.Status{{
		State:         svc.Stopped,
		Win32ExitCode: uint32(winapi.ERROR_PROCESS_ABORTED),
	}}}
	_, err := waitForServiceState(service, svc.Running, time.Second)
	if !errors.Is(err, syscall.Errno(winapi.ERROR_PROCESS_ABORTED)) {
		t.Fatalf("error = %v, want %v", err, winapi.ERROR_PROCESS_ABORTED)
	}
}

func TestStopAndWaitDoesNotStopAgainWhilePending(t *testing.T) {
	service := &fakeWindowsService{statuses: []svc.Status{
		{State: svc.StopPending},
		{State: svc.Stopped},
	}}
	if err := stopAndWait(service, DefaultStopTimeout); err != nil {
		t.Fatal(err)
	}
	if service.controlCalls != 0 {
		t.Fatalf("Control calls = %d, want 0", service.controlCalls)
	}
}

func TestStopAndWaitReportsAlreadyStopped(t *testing.T) {
	service := &fakeWindowsService{statuses: []svc.Status{{State: svc.Stopped}}}
	if err := stopAndWait(service, DefaultStopTimeout); !errors.Is(err, winapi.ERROR_SERVICE_NOT_ACTIVE) {
		t.Fatalf("error = %v, want %v", err, winapi.ERROR_SERVICE_NOT_ACTIVE)
	}
}

func TestStopAndWaitUsesConfiguredTimeoutWithoutKilling(t *testing.T) {
	service := &fakeWindowsService{statuses: []svc.Status{{State: svc.Running}}}
	err := stopAndWait(service, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out waiting for service state") {
		t.Fatalf("error = %v, want timeout", err)
	}
	if service.controlCalls != 1 {
		t.Fatalf("Control calls = %d, want 1", service.controlCalls)
	}
}

func TestValidateExecutableRejectsInvalidWindowsFiles(t *testing.T) {
	err := validateExecutable(t.TempDir())
	if !errors.Is(err, ErrInvalidExecutablePath) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidExecutablePath)
	}

	target := t.TempDir() + `\service.exe`
	if err := os.WriteFile(target, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateExecutable(target); !errors.Is(err, ErrInvalidExecutablePath) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidExecutablePath)
	}
	if err := os.WriteFile(target, []byte{'M', 'Z', 0, 0}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateExecutable(target); err != nil {
		t.Fatalf("validateExecutable(magic-only file) error = %v", err)
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateExecutable(executable); err != nil {
		t.Fatalf("validateExecutable(native executable) error = %v", err)
	}
}

func TestWindowsServiceStatus(t *testing.T) {
	tests := []struct {
		state svc.State
		want  string
	}{
		{state: svc.Stopped, want: ServiceStopped},
		{state: svc.StartPending, want: "starting"},
		{state: svc.StopPending, want: "stopping"},
		{state: svc.Running, want: ServiceRunning},
		{state: svc.ContinuePending, want: "continuing"},
		{state: svc.PausePending, want: "pausing"},
		{state: svc.Paused, want: "paused"},
		{state: svc.State(99), want: "unknown"},
	}

	for _, test := range tests {
		if got := windowsServiceStatus(test.state); got != test.want {
			t.Fatalf("windowsServiceStatus(%d) = %q, want %q", test.state, got, test.want)
		}
	}
}
