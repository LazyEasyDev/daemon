//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

type windowsNativeServiceHandler struct {
	app *application
}

func (handler *windowsNativeServiceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}
	if err := handler.app.start(); err != nil {
		return true, 1
	}
	handler.app.startStopAfterTimer()
	status := svc.Status{
		State:   svc.Running,
		Accepts: svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPreShutdown,
	}
	changes <- status

	for request := range requests {
		switch request.Cmd {
		case svc.Interrogate:
			changes <- status
		case svc.Stop, svc.Shutdown, svc.PreShutdown:
			changes <- svc.Status{
				State:    svc.StopPending,
				WaitHint: uint32((handler.app.config.StopDelay + 5*time.Second) / time.Millisecond),
			}
			handler.app.Stop()
			return false, 0
		}
	}
	return false, 0
}

func runApplication(app *application, windowsNativeService bool) error {
	if !windowsNativeService {
		return app.run()
	}
	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if !isService {
		return errors.New("windows-native-service must be started by Windows SCM")
	}
	serviceName, err := currentWindowsServiceName()
	if err != nil {
		return err
	}
	return svc.Run(serviceName, &windowsNativeServiceHandler{app: app})
}

func currentWindowsServiceName() (string, error) {
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT|windows.SC_MANAGER_ENUMERATE_SERVICE)
	if err != nil {
		return "", err
	}
	defer windows.CloseServiceHandle(manager)

	var bytesNeeded uint32
	var servicesReturned uint32
	var buffer []byte
	for {
		var data *byte
		if len(buffer) > 0 {
			data = &buffer[0]
		}
		err = windows.EnumServicesStatusEx(
			manager,
			windows.SC_ENUM_PROCESS_INFO,
			windows.SERVICE_WIN32_OWN_PROCESS,
			windows.SERVICE_ACTIVE,
			data,
			uint32(len(buffer)),
			&bytesNeeded,
			&servicesReturned,
			nil,
			nil,
		)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.ERROR_MORE_DATA) || bytesNeeded <= uint32(len(buffer)) {
			return "", err
		}
		buffer = make([]byte, bytesNeeded)
	}

	processID := uint32(os.Getpid())
	if servicesReturned > 0 {
		statuses := unsafe.Slice((*windows.ENUM_SERVICE_STATUS_PROCESS)(unsafe.Pointer(&buffer[0])), int(servicesReturned))
		for _, status := range statuses {
			if status.ServiceStatusProcess.ProcessId == processID {
				return windows.UTF16PtrToString(status.ServiceName), nil
			}
		}
	}
	return "", fmt.Errorf("service registration for process %d was not found", processID)
}
