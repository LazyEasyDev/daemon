//go:build windows

package daemon_util

import (
	"time"

	"golang.org/x/sys/windows/svc"
)

const (
	windowsPendingUpdateInterval = time.Second
	windowsPendingWaitHint       = 10 * time.Second
)

type serviceHandler struct {
	executable            Executable
	pendingUpdateInterval time.Duration
}

type completionExecutable interface {
	Done() <-chan error
}

func (sh *serviceHandler) updateInterval() time.Duration {
	if sh.pendingUpdateInterval > 0 {
		return sh.pendingUpdateInterval
	}
	return windowsPendingUpdateInterval
}

func runWindowsPendingOperation(changes chan<- svc.Status, state svc.State, operation func(), updateInterval time.Duration) {
	checkpoint := uint32(1)
	pendingStatus := func() svc.Status {
		return svc.Status{
			State:      state,
			CheckPoint: checkpoint,
			WaitHint:   uint32(windowsPendingWaitHint / time.Millisecond),
		}
	}
	changes <- pendingStatus()

	done := make(chan struct{})
	go func() {
		operation()
		close(done)
	}()

	ticker := time.NewTicker(updateInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			checkpoint++
			changes <- pendingStatus()
		}
	}
}

func (sh *serviceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const commandsAccepted = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPreShutdown

	runWindowsPendingOperation(changes, svc.StartPending, sh.executable.Start, sh.updateInterval())
	var done <-chan error
	if executable, ok := sh.executable.(completionExecutable); ok {
		done = executable.Done()
		select {
		case err := <-done:
			if err != nil {
				return true, 1
			}
			return false, 0
		default:
		}
	}
	changes <- svc.Status{State: svc.Running, Accepts: commandsAccepted}

	for {
		select {
		case err := <-done:
			if err != nil {
				return true, 1
			}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				changes <- request.CurrentStatus
				// Testing deadlock from https://code.google.com/p/winsvc/issues/detail?id=4
				time.Sleep(100 * time.Millisecond)
				changes <- request.CurrentStatus
			case svc.Stop, svc.Shutdown, svc.PreShutdown:
				runWindowsPendingOperation(changes, svc.StopPending, sh.executable.Stop, sh.updateInterval())
				return false, 0
			}
		}
	}
}

func (windows *windowsRecord) Run(executable Executable) (string, error) {
	runAction := "Running " + windows.description + ":"

	isService, err := svc.IsWindowsService()
	if err != nil {
		return runAction + failed, getWindowsError(err)
	}
	if isService {
		if err := svc.Run(windows.name, &serviceHandler{executable: executable}); err != nil {
			return runAction + failed, getWindowsError(err)
		}
	} else {
		executable.Run()
	}

	return runAction + " completed.", nil
}
