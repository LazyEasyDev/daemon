//go:build windows

package daemon_util

import (
	"os/exec"
	"path/filepath"
)

type windowsCommandExecutable struct {
	command *exec.Cmd
	done    chan error
}

func newWindowsCommandExecutable(path string, args ...string) *windowsCommandExecutable {
	command := exec.Command(path, args...)
	command.Dir = filepath.Dir(path)
	return &windowsCommandExecutable{
		command: command,
		done:    make(chan error, 1),
	}
}

func (executable *windowsCommandExecutable) Start() {
	if err := executable.command.Start(); err != nil {
		executable.done <- err
		return
	}
	go func() {
		executable.done <- executable.command.Wait()
	}()
}

func (executable *windowsCommandExecutable) Stop() {
	if executable.command.Process != nil {
		_ = executable.command.Process.Kill()
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
