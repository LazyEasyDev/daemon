//go:build windows

package main

import (
	"context"
	"errors"
	"os"

	"github.com/urfave/cli/v3"
	"golang.org/x/sys/windows"

	"github.com/LazyEasyDev/daemon/daemon_util"
)

const windowsServiceCommand = "run-windows-service"

func configuredInstallService(command *cli.Command, serviceName, executablePath string) (daemon_util.Daemon, []string, error) {
	if err := daemon_util.ValidateExecutablePath(executablePath); err != nil {
		return nil, nil, err
	}

	wrapperPath, err := daemon_util.ExecPath()
	if err != nil {
		return nil, nil, err
	}
	service, err := configuredService(command, serviceName, wrapperPath)
	if err != nil {
		return nil, nil, err
	}
	return service, []string{windowsServiceCommand, serviceName, executablePath}, nil
}

func platformCommands() []*cli.Command {
	stopAfterExecutable := 2
	return []*cli.Command{{
		Name:         windowsServiceCommand,
		Hidden:       true,
		StopOnNthArg: &stopAfterExecutable,
		Action:       runWindowsService,
	}}
}

func runWindowsService(_ context.Context, command *cli.Command) error {
	args := command.Args().Slice()
	if len(args) < 2 {
		return errors.New("Windows service runner requires a service name and executable")
	}
	registrationName, err := daemon_util.ManagedServiceName(args[0])
	if err != nil {
		return err
	}
	_, err = daemon_util.RunWindowsCommandService(registrationName, args[0], args[1], args[2:]...)
	return err
}

func isTerminal(file *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(file.Fd()), &mode) == nil
}
