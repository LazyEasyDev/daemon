// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by
// license that can be found in the LICENSE file.

//go:build windows

// Package daemon windows version
package daemon_util

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	winapi "golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// windowsRecord - standard record (struct) for windows version of daemon package
type windowsRecord struct {
	serviceConfig
	name           string
	description    string
	dependencies   []string
	executablePath string
}

type windowsService struct {
	name      string
	processID uint32
	state     svc.State
}

type windowsPreshutdownInfo struct {
	timeoutMilliseconds uint32
}

const (
	windowsPendingUpdateInterval = time.Second
	windowsPendingWaitHint       = 10 * time.Second
	maxWindowsPreshutdownTimeout = time.Duration(1<<32-1) * time.Millisecond
)

func newDaemon(name, description string, _ Kind, dependencies []string, executablePath string) (Daemon, error) {

	return &windowsRecord{
		name:           name,
		description:    description,
		dependencies:   dependencies,
		executablePath: executablePath,
	}, nil
}

func connectServiceManager(access uint32) (*mgr.Mgr, error) {
	handle, err := winapi.OpenSCManager(nil, nil, access)
	if err != nil {
		return nil, err
	}
	return &mgr.Mgr{Handle: handle}, nil
}

func openWindowsService(manager *mgr.Mgr, name string, access uint32) (*mgr.Service, error) {
	namePointer, err := winapi.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	handle, err := winapi.OpenService(manager.Handle, namePointer, access)
	if err != nil {
		return nil, err
	}
	return &mgr.Service{Name: name, Handle: handle}, nil
}

func windowsPreshutdownTimeoutMilliseconds(timeout time.Duration) (uint32, error) {
	if timeout < time.Millisecond || timeout > maxWindowsPreshutdownTimeout {
		return 0, fmt.Errorf("%w: exceeds the Windows preshutdown limit", ErrInvalidStopTimeout)
	}
	return uint32(timeout / time.Millisecond), nil
}

func setWindowsPreshutdownTimeout(service *mgr.Service, timeout time.Duration) error {
	timeoutMilliseconds, err := windowsPreshutdownTimeoutMilliseconds(timeout)
	if err != nil {
		return err
	}
	info := windowsPreshutdownInfo{timeoutMilliseconds: timeoutMilliseconds}
	return winapi.ChangeServiceConfig2(
		service.Handle,
		winapi.SERVICE_CONFIG_PRESHUTDOWN_INFO,
		(*byte)(unsafe.Pointer(&info)),
	)
}

// ListServices returns user-facing names of services registered by this tool.
func ListServices() ([]string, error) {
	services, err := enumerateWindowsServices(winapi.SERVICE_STATE_ALL)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(services))
	for _, service := range services {
		names = append(names, service.name)
	}
	return filterManagedServiceNames(names), nil
}

// ListServiceStatuses returns the status of services registered by this tool.
func ListServiceStatuses() ([]ServiceStatus, error) {
	services, err := enumerateWindowsServices(winapi.SERVICE_STATE_ALL)
	if err != nil {
		return nil, err
	}
	statuses := make(map[string]string)
	for _, service := range services {
		logicalName, ok := logicalServiceName(service.name)
		if ok {
			statuses[logicalName] = windowsServiceStatus(service.state)
		}
	}
	return sortedServiceStatuses(statuses), nil
}

func currentWindowsServiceName() (string, error) {
	services, err := enumerateWindowsServices(winapi.SERVICE_ACTIVE)
	if err != nil {
		return "", err
	}
	processID := uint32(os.Getpid())
	for _, service := range services {
		if service.processID == processID {
			return service.name, nil
		}
	}
	return "", fmt.Errorf("service registration for process %d was not found", processID)
}

func enumerateWindowsServices(state uint32) ([]windowsService, error) {
	manager, err := connectServiceManager(winapi.SC_MANAGER_CONNECT | winapi.SC_MANAGER_ENUMERATE_SERVICE)
	if err != nil {
		return nil, getWindowsError(err)
	}
	defer manager.Disconnect()

	var bytesNeeded uint32
	var servicesReturned uint32
	var buffer []byte
	for {
		var data *byte
		if len(buffer) > 0 {
			data = &buffer[0]
		}
		err = winapi.EnumServicesStatusEx(
			manager.Handle,
			winapi.SC_ENUM_PROCESS_INFO,
			winapi.SERVICE_WIN32_OWN_PROCESS,
			state,
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
			return nil, getWindowsError(err)
		}
		buffer = make([]byte, bytesNeeded)
	}
	if servicesReturned == 0 {
		return nil, nil
	}

	statuses := unsafe.Slice((*winapi.ENUM_SERVICE_STATUS_PROCESS)(unsafe.Pointer(&buffer[0])), int(servicesReturned))
	services := make([]windowsService, 0, len(statuses))
	for _, status := range statuses {
		services = append(services, windowsService{
			name:      winapi.UTF16PtrToString(status.ServiceName),
			processID: status.ServiceStatusProcess.ProcessId,
			state:     svc.State(status.ServiceStatusProcess.CurrentState),
		})
	}
	return services, nil
}

// Install the service
func (windows *windowsRecord) Install(args ...string) (string, error) {
	installAction := "Install " + windows.description + ":"

	execp, err := resolveExecutablePath(windows.name, windows.executablePath)
	if err != nil {
		return installAction + failed, err
	}

	m, err := connectServiceManager(winapi.SC_MANAGER_CONNECT | winapi.SC_MANAGER_CREATE_SERVICE)
	if err != nil {
		return installAction + failed, getWindowsError(err)
	}
	defer m.Disconnect()

	s, err := openWindowsService(m, windows.name, winapi.SERVICE_QUERY_STATUS)
	if err == nil {
		s.Close()
		return installAction + failed, ErrAlreadyInstalled
	}
	if !errors.Is(err, winapi.ERROR_SERVICE_DOES_NOT_EXIST) {
		return installAction + failed, getWindowsError(err)
	}

	s, err = m.CreateService(windows.name, execp, mgr.Config{
		DisplayName:  windows.description,
		Description:  windows.description,
		StartType:    mgr.StartAutomatic,
		Dependencies: windows.dependencies,
	}, args...)
	if err != nil {
		return installAction + failed, getWindowsError(err)
	}
	defer s.Close()

	// set recovery action for service
	// restart after 5 seconds for the first 3 times
	// restart after 1 minute, otherwise
	recoveryActions := []mgr.RecoveryAction{
		{
			Type:  mgr.ServiceRestart,
			Delay: 5000 * time.Millisecond,
		},
		{
			Type:  mgr.ServiceRestart,
			Delay: 5000 * time.Millisecond,
		},
		{
			Type:  mgr.ServiceRestart,
			Delay: 5000 * time.Millisecond,
		},
		{
			Type:  mgr.ServiceRestart,
			Delay: 60000 * time.Millisecond,
		},
	}
	// set reset period as a day
	if err := s.SetRecoveryActions(recoveryActions, uint32(86400)); err != nil {
		recoveryErr := getWindowsError(err)
		if rollbackErr := s.Delete(); rollbackErr != nil {
			return installAction + failed, errors.Join(
				recoveryErr,
				fmt.Errorf("rollback service creation: %w", getWindowsError(rollbackErr)),
			)
		}
		return installAction + failed, recoveryErr
	}
	if err := setWindowsPreshutdownTimeout(s, windows.stopTimeoutDuration()); err != nil {
		preshutdownErr := getWindowsError(err)
		if rollbackErr := s.Delete(); rollbackErr != nil {
			return installAction + failed, errors.Join(
				preshutdownErr,
				fmt.Errorf("rollback service creation: %w", getWindowsError(rollbackErr)),
			)
		}
		return installAction + failed, preshutdownErr
	}

	return installAction + " completed.", nil
}

// Remove the service
func (windows *windowsRecord) Remove() (string, error) {
	removeAction := "Removing " + windows.description + ":"

	m, err := connectServiceManager(winapi.SC_MANAGER_CONNECT)
	if err != nil {
		return removeAction + failed, getWindowsError(err)
	}
	defer m.Disconnect()
	s, err := openWindowsService(m, windows.name, winapi.DELETE)
	if err != nil {
		return removeAction + failed, getWindowsError(err)
	}
	defer s.Close()
	err = s.Delete()
	if err != nil {
		return removeAction + failed, getWindowsError(err)
	}

	return removeAction + " completed.", nil
}

// Start the service
func (windows *windowsRecord) Start() (string, error) {
	startAction := "Starting " + windows.description + ":"

	m, err := connectServiceManager(winapi.SC_MANAGER_CONNECT)
	if err != nil {
		return startAction + failed, getWindowsError(err)
	}
	defer m.Disconnect()
	s, err := openWindowsService(m, windows.name, winapi.SERVICE_START|winapi.SERVICE_QUERY_STATUS)
	if err != nil {
		return startAction + failed, getWindowsError(err)
	}
	defer s.Close()
	if err = s.Start(); err != nil {
		return startAction + failed, getWindowsError(err)
	}
	if _, err := waitForServiceState(s, svc.Running, 30*time.Second); err != nil {
		return startAction + failed, getWindowsError(err)
	}

	return startAction + " completed.", nil
}

// Stop the service
func (windows *windowsRecord) Stop() (string, error) {
	stopAction := "Stopping " + windows.description + ":"

	m, err := connectServiceManager(winapi.SC_MANAGER_CONNECT)
	if err != nil {
		return stopAction + failed, getWindowsError(err)
	}
	defer m.Disconnect()
	s, err := openWindowsService(m, windows.name, winapi.SERVICE_STOP|winapi.SERVICE_QUERY_STATUS)
	if err != nil {
		return stopAction + failed, getWindowsError(err)
	}
	defer s.Close()
	if err := stopAndWait(s, windows.stopTimeoutDuration()); err != nil {
		return stopAction + failed, getWindowsError(err)
	}

	return stopAction + " completed.", nil
}

type serviceStatusQuerier interface {
	Query() (svc.Status, error)
}

type serviceController interface {
	serviceStatusQuerier
	Control(svc.Cmd) (svc.Status, error)
}

func stopAndWait(service serviceController, timeout time.Duration) error {
	status, err := service.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Stopped {
		return winapi.ERROR_SERVICE_NOT_ACTIVE
	}
	if status.State != svc.StopPending {
		if _, err := service.Control(svc.Stop); err != nil {
			return err
		}
	}

	_, err = waitForServiceState(service, svc.Stopped, timeout+100*time.Millisecond)
	return err
}

func waitForServiceState(service serviceStatusQuerier, target svc.State, timeout time.Duration) (svc.Status, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()

	for {
		status, err := service.Query()
		if err != nil {
			return svc.Status{}, err
		}
		if status.State == target {
			return status, nil
		}
		if target == svc.Running && status.State == svc.Stopped {
			if status.Win32ExitCode != 0 {
				return status, syscall.Errno(status.Win32ExitCode)
			}
			return status, errors.New("service stopped before reaching running state")
		}

		select {
		case <-tick.C:
		case <-timer.C:
			return status, fmt.Errorf("timed out waiting for service state %s", getWindowsServiceStateFromUint32(target))
		}
	}
}

// Status - Get service status
func (windows *windowsRecord) Status() (string, error) {
	m, err := connectServiceManager(winapi.SC_MANAGER_CONNECT)
	if err != nil {
		return "Getting status:" + failed, getWindowsError(err)
	}
	defer m.Disconnect()
	s, err := openWindowsService(m, windows.name, winapi.SERVICE_QUERY_STATUS)
	if err != nil {
		return "Getting status:" + failed, getWindowsError(err)
	}
	defer s.Close()
	status, err := s.Query()
	if err != nil {
		return "Getting status:" + failed, getWindowsError(err)
	}

	return "Status: " + getWindowsServiceStateFromUint32(status.State), nil
}

// Get windows error
func getWindowsError(inputError error) error {
	var errno syscall.Errno
	if errors.As(inputError, &errno) {
		return windowsErrorFromCode(int(errno), inputError)
	}

	if exiterr, ok := inputError.(*exec.ExitError); ok {
		if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
			return windowsErrorFromCode(status.ExitStatus(), inputError)
		}
	}

	return inputError
}

func windowsErrorFromCode(code int, fallback error) error {
	switch code {
	case int(winapi.ERROR_SERVICE_DOES_NOT_EXIST):
		return ErrNotInstalled
	case int(winapi.ERROR_SERVICE_EXISTS):
		return ErrAlreadyInstalled
	case int(winapi.ERROR_SERVICE_ALREADY_RUNNING):
		return ErrAlreadyRunning
	case int(winapi.ERROR_SERVICE_NOT_ACTIVE):
		return ErrAlreadyStopped
	}

	if systemError, ok := WinErrCode[code]; ok {
		return fmt.Errorf("%s: %s %s", systemError.Title, systemError.Description, systemError.Action)
	}
	return fallback
}

// Get windows service state
func getWindowsServiceStateFromUint32(state svc.State) string {
	switch state {
	case svc.Stopped:
		return "SERVICE_STOPPED"
	case svc.StartPending:
		return "SERVICE_START_PENDING"
	case svc.StopPending:
		return "SERVICE_STOP_PENDING"
	case svc.Running:
		return "SERVICE_RUNNING"
	case svc.ContinuePending:
		return "SERVICE_CONTINUE_PENDING"
	case svc.PausePending:
		return "SERVICE_PAUSE_PENDING"
	case svc.Paused:
		return "SERVICE_PAUSED"
	}
	return "SERVICE_UNKNOWN"
}

func windowsServiceStatus(state svc.State) string {
	switch state {
	case svc.Stopped:
		return ServiceStopped
	case svc.StartPending:
		return "starting"
	case svc.StopPending:
		return "stopping"
	case svc.Running:
		return ServiceRunning
	case svc.ContinuePending:
		return "continuing"
	case svc.PausePending:
		return "pausing"
	case svc.Paused:
		return "paused"
	default:
		return "unknown"
	}
}

type serviceHandler struct {
	executable            Executable
	pendingUpdateInterval time.Duration
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

func (sh *serviceHandler) Execute(_ []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPreShutdown

	runWindowsPendingOperation(changes, svc.StartPending, sh.executable.Start, sh.updateInterval())
	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

loop:
	for {
		request := <-r
		switch request.Cmd {
		case svc.Interrogate:
			changes <- request.CurrentStatus
			// Testing deadlock from https://code.google.com/p/winsvc/issues/detail?id=4
			time.Sleep(100 * time.Millisecond)
			changes <- request.CurrentStatus
		case svc.Stop, svc.Shutdown, svc.PreShutdown:
			runWindowsPendingOperation(changes, svc.StopPending, sh.executable.Stop, sh.updateInterval())
			break loop
		}
	}
	return false, 0
}

func (windows *windowsRecord) Run(e Executable) (string, error) {
	runAction := "Running " + windows.description + ":"

	isService, err := svc.IsWindowsService()
	if err != nil {
		return runAction + failed, getWindowsError(err)
	}
	if isService {
		serviceName, err := currentWindowsServiceName()
		if err != nil {
			return runAction + failed, err
		}
		err = svc.Run(serviceName, &serviceHandler{
			executable: e,
		})
		if err != nil {
			return runAction + failed, getWindowsError(err)
		}
	} else {
		// otherwise, service was called outside the service manager
		e.Run()
	}

	return runAction + " completed.", nil
}

// GetTemplate - gets service config template
func (linux *windowsRecord) GetTemplate() string {
	return ""
}

// SetTemplate - sets service config template
func (linux *windowsRecord) SetTemplate(tplStr string) error {
	return errors.New("templating is not supported for windows")
}
