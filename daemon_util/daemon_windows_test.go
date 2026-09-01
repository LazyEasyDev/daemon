//go:build windows

package daemon_util

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	winapi "golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

type fakeWindowsService struct {
	statuses     []svc.Status
	controlCalls int
	controlErr   error
}

type fakeWindowsExecutable struct {
	stop     func()
	startErr error
	stopErr  error
}

type completingWindowsExecutable struct {
	done chan error
}

func (executable *fakeWindowsExecutable) Start() error {
	return executable.startErr
}

func (executable *fakeWindowsExecutable) Stop() error {
	if executable.stop != nil {
		executable.stop()
	}
	return executable.stopErr
}

func (*fakeWindowsExecutable) Run() error { return nil }

func (*completingWindowsExecutable) Start() error { return nil }

func (*completingWindowsExecutable) Stop() error { return nil }

func (*completingWindowsExecutable) Run() error { return nil }

func (executable *completingWindowsExecutable) Done() <-chan error {
	return executable.done
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

func TestWindowsPreshutdownTimeoutMilliseconds(t *testing.T) {
	got, err := windowsPreshutdownTimeoutMilliseconds(3 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got != 180000 {
		t.Fatalf("timeout = %d ms, want 180000 ms", got)
	}

	_, err = windowsPreshutdownTimeoutMilliseconds(time.Nanosecond)
	if !errors.Is(err, ErrInvalidStopTimeout) {
		t.Fatalf("sub-millisecond error = %v, want %v", err, ErrInvalidStopTimeout)
	}

	_, err = windowsPreshutdownTimeoutMilliseconds(maxWindowsPreshutdownTimeout + time.Millisecond)
	if !errors.Is(err, ErrInvalidStopTimeout) {
		t.Fatalf("overflow error = %v, want %v", err, ErrInvalidStopTimeout)
	}
}

func TestServiceHandlerReportsPreshutdownProgress(t *testing.T) {
	stopStarted := make(chan struct{})
	finishStop := make(chan struct{})
	executable := &fakeWindowsExecutable{stop: func() {
		close(stopStarted)
		<-finishStop
	}}
	handler := &serviceHandler{
		executable:            executable,
		pendingUpdateInterval: time.Millisecond,
	}
	requests := make(chan svc.ChangeRequest, 1)
	changes := make(chan svc.Status, 8)
	done := make(chan struct{})
	go func() {
		handler.Execute(nil, requests, changes)
		close(done)
	}()

	startPending := <-changes
	if startPending.State != svc.StartPending || startPending.CheckPoint != 1 || startPending.WaitHint == 0 {
		t.Fatalf("start pending status = %+v", startPending)
	}
	running := <-changes
	if running.State != svc.Running || running.Accepts&svc.AcceptPreShutdown == 0 {
		t.Fatalf("running status = %+v, want preshutdown accepted", running)
	}

	requests <- svc.ChangeRequest{Cmd: svc.PreShutdown}
	<-stopStarted
	firstStopPending := <-changes
	secondStopPending := <-changes
	if firstStopPending.State != svc.StopPending || firstStopPending.CheckPoint != 1 || firstStopPending.WaitHint == 0 {
		t.Fatalf("first stop pending status = %+v", firstStopPending)
	}
	if secondStopPending.State != svc.StopPending || secondStopPending.CheckPoint <= firstStopPending.CheckPoint {
		t.Fatalf("second stop pending status = %+v, want checkpoint after %d", secondStopPending, firstStopPending.CheckPoint)
	}

	close(finishStop)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("service handler did not finish after preshutdown")
	}
}

func TestServiceHandlerReportsChildFailure(t *testing.T) {
	executable := &completingWindowsExecutable{done: make(chan error, 1)}
	handler := &serviceHandler{executable: executable}
	requests := make(chan svc.ChangeRequest)
	changes := make(chan svc.Status, 2)
	result := make(chan struct {
		specific bool
		code     uint32
	}, 1)
	go func() {
		specific, code := handler.Execute(nil, requests, changes)
		result <- struct {
			specific bool
			code     uint32
		}{specific: specific, code: code}
	}()

	<-changes
	<-changes
	executable.done <- errors.New("child failed")
	got := <-result
	if !got.specific || got.code == 0 {
		t.Fatalf("service result = (%t, %d), want a nonzero service-specific failure", got.specific, got.code)
	}
}

func TestServiceHandlerReportsStartFailure(t *testing.T) {
	handler := &serviceHandler{
		executable: &fakeWindowsExecutable{startErr: errors.New("start failed")},
	}
	changes := make(chan svc.Status, 1)
	specific, code := handler.Execute(nil, make(chan svc.ChangeRequest), changes)
	if !specific || code == 0 {
		t.Fatalf("service result = (%t, %d), want a nonzero service-specific failure", specific, code)
	}
	if status := <-changes; status.State != svc.StartPending {
		t.Fatalf("service status = %v, want %v", status.State, svc.StartPending)
	}
}

func TestServiceHandlerReportsStopFailure(t *testing.T) {
	handler := &serviceHandler{
		executable: &fakeWindowsExecutable{stopErr: errors.New("stop failed")},
	}
	requests := make(chan svc.ChangeRequest, 1)
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	changes := make(chan svc.Status, 3)
	specific, code := handler.Execute(nil, requests, changes)
	if !specific || code == 0 {
		t.Fatalf("service result = (%t, %d), want a nonzero service-specific failure", specific, code)
	}
}

func TestWindowsCommandStartsSuspended(t *testing.T) {
	path := `C:\service.exe`
	executable := newWindowsCommandExecutable(path, DefaultStopTimeout)
	if executable.command.SysProcAttr.CreationFlags&winapi.CREATE_SUSPENDED == 0 {
		t.Fatal("wrapped Windows command must start suspended before Job Object assignment")
	}
	if executable.command.SysProcAttr.CreationFlags&winapi.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatal("wrapped Windows command must start in a console process group")
	}
	if executable.command.Dir != filepath.Dir(path) {
		t.Fatalf("working directory = %q, want %q", executable.command.Dir, filepath.Dir(path))
	}
}

func TestWaitForWindowsProcessGroupStopSendsBreakAndWaitsForExit(t *testing.T) {
	done := make(chan error, 1)
	done <- nil
	var event uint32
	var processGroupID uint32
	stopped := waitForWindowsProcessGroupStop(42, done, time.Second, func(gotEvent, gotProcessGroupID uint32) error {
		event = gotEvent
		processGroupID = gotProcessGroupID
		return nil
	})
	if !stopped {
		t.Fatal("process group did not stop after CTRL+BREAK")
	}
	if event != winapi.CTRL_BREAK_EVENT || processGroupID != 42 {
		t.Fatalf("console event = (%d, %d), want (%d, 42)", event, processGroupID, winapi.CTRL_BREAK_EVENT)
	}
}

func TestWaitForWindowsProcessGroupStopFallsBackOnDeliveryFailure(t *testing.T) {
	deliveryErr := errors.New("delivery failed")
	stopped := waitForWindowsProcessGroupStop(42, make(chan error), time.Second, func(uint32, uint32) error {
		return deliveryErr
	})
	if stopped {
		t.Fatal("process group reported stopped after CTRL+BREAK delivery failure")
	}
}

func TestWaitForWindowsProcessGroupStopFallsBackAfterTimeout(t *testing.T) {
	stopped := waitForWindowsProcessGroupStop(42, make(chan error), time.Millisecond, func(uint32, uint32) error {
		return nil
	})
	if stopped {
		t.Fatal("process group reported stopped before graceful-stop timeout")
	}
}

func TestWindowsKillJobTerminatesProcessesOnClose(t *testing.T) {
	job, err := createWindowsKillJob()
	if err != nil {
		t.Fatal(err)
	}
	defer winapi.CloseHandle(job)

	information := winapi.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	if err := winapi.QueryInformationJobObject(
		job,
		winapi.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)),
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if information.BasicLimitInformation.LimitFlags&winapi.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE == 0 {
		t.Fatal("Windows Job Object must terminate contained processes when closed")
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
