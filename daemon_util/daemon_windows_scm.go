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
	executable            windowsServiceExecutable
	pendingUpdateInterval time.Duration
}

type windowsServiceExecutable interface {
	Start() error
	Stop() error
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

func runWindowsPendingOperation(changes chan<- svc.Status, state svc.State, operation func() error, updateInterval time.Duration) error {
	checkpoint := uint32(1)
	pendingStatus := func() svc.Status {
		return svc.Status{
			State:      state,
			CheckPoint: checkpoint,
			WaitHint:   uint32(windowsPendingWaitHint / time.Millisecond),
		}
	}
	changes <- pendingStatus()

	done := make(chan error, 1)
	go func() {
		done <- operation()
	}()

	ticker := time.NewTicker(updateInterval)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-ticker.C:
			checkpoint++
			changes <- pendingStatus()
		}
	}
}

func (sh *serviceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const commandsAccepted = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPreShutdown

	if err := runWindowsPendingOperation(changes, svc.StartPending, sh.executable.Start, sh.updateInterval()); err != nil {
		return true, 1
	}
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
				if err := runWindowsPendingOperation(changes, svc.StopPending, sh.executable.Stop, sh.updateInterval()); err != nil {
					return true, 1
				}
				return false, 0
			}
		}
	}
}
