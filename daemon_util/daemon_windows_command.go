//go:build windows

package daemon_util

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"

	winapi "golang.org/x/sys/windows"
)

type windowsCommandExecutable struct {
	command *exec.Cmd
	done    chan error
	jobMu   sync.Mutex
	job     winapi.Handle
}

func newWindowsCommandExecutable(path string, args ...string) *windowsCommandExecutable {
	command := exec.Command(path, args...)
	command.Dir = filepath.Dir(path)
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: winapi.CREATE_SUSPENDED}
	return &windowsCommandExecutable{
		command: command,
		done:    make(chan error, 1),
	}
}

func (executable *windowsCommandExecutable) Start() {
	job, err := createWindowsKillJob()
	if err != nil {
		executable.done <- err
		return
	}
	executable.jobMu.Lock()
	executable.job = job
	executable.jobMu.Unlock()

	if err := executable.command.Start(); err != nil {
		executable.closeJob()
		executable.done <- err
		return
	}
	if err := executable.assignAndResume(); err != nil {
		_ = executable.command.Process.Kill()
		_ = executable.command.Wait()
		executable.closeJob()
		executable.done <- err
		return
	}
	go func() {
		err := executable.command.Wait()
		executable.closeJob()
		executable.done <- err
	}()
}

func (executable *windowsCommandExecutable) Stop() {
	executable.jobMu.Lock()
	defer executable.jobMu.Unlock()
	if executable.job != 0 {
		_ = winapi.TerminateJobObject(executable.job, 1)
	}
}

func createWindowsKillJob() (winapi.Handle, error) {
	job, err := winapi.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("create child process job: %w", err)
	}
	information := winapi.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	information.BasicLimitInformation.LimitFlags = winapi.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := winapi.SetInformationJobObject(
		job,
		winapi.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)),
	); err != nil {
		_ = winapi.CloseHandle(job)
		return 0, fmt.Errorf("configure child process job: %w", err)
	}
	return job, nil
}

func (executable *windowsCommandExecutable) assignAndResume() error {
	processID := uint32(executable.command.Process.Pid)
	process, err := winapi.OpenProcess(winapi.PROCESS_SET_QUOTA|winapi.PROCESS_TERMINATE, false, processID)
	if err != nil {
		return fmt.Errorf("open suspended child process: %w", err)
	}
	defer winapi.CloseHandle(process)

	executable.jobMu.Lock()
	job := executable.job
	executable.jobMu.Unlock()
	if err := winapi.AssignProcessToJobObject(job, process); err != nil {
		return fmt.Errorf("assign child process to job: %w", err)
	}

	thread, err := openWindowsProcessThread(processID)
	if err != nil {
		return fmt.Errorf("open suspended child thread: %w", err)
	}
	defer winapi.CloseHandle(thread)
	if _, err := winapi.ResumeThread(thread); err != nil {
		return fmt.Errorf("resume child process: %w", err)
	}
	return nil
}

func openWindowsProcessThread(processID uint32) (winapi.Handle, error) {
	snapshot, err := winapi.CreateToolhelp32Snapshot(winapi.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return 0, err
	}
	defer winapi.CloseHandle(snapshot)

	entry := winapi.ThreadEntry32{Size: uint32(unsafe.Sizeof(winapi.ThreadEntry32{}))}
	if err := winapi.Thread32First(snapshot, &entry); err != nil {
		return 0, err
	}
	for {
		if entry.OwnerProcessID == processID {
			return winapi.OpenThread(winapi.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		}
		if err := winapi.Thread32Next(snapshot, &entry); err != nil {
			return 0, err
		}
	}
}

func (executable *windowsCommandExecutable) closeJob() {
	executable.jobMu.Lock()
	defer executable.jobMu.Unlock()
	if executable.job != 0 {
		_ = winapi.CloseHandle(executable.job)
		executable.job = 0
	}
}

func (executable *windowsCommandExecutable) Run() {
	executable.Start()
	<-executable.done
}

func (executable *windowsCommandExecutable) Done() <-chan error {
	return executable.done
}

// RunWindowsCommandService hosts an ordinary executable behind the Windows
// Service Control Manager protocol.
func RunWindowsCommandService(name, description, path string, args ...string) (string, error) {
	service := &windowsRecord{name: name, description: description}
	return service.Run(newWindowsCommandExecutable(path, args...))
}
