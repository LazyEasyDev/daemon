//go:build linux

package main

import (
	"os"

	"github.com/LazyEasyDev/daemon/daemon_util"
	"github.com/urfave/cli/v3"
	"golang.org/x/sys/unix"
)

func configuredInstallService(command *cli.Command, serviceName, executablePath string) (daemon_util.Daemon, []string, error) {
	service, err := configuredService(command, serviceName, executablePath)
	return service, nil, err
}

func platformCommands() []*cli.Command {
	return nil
}

func isTerminal(file *os.File) bool {
	_, err := unix.IoctlGetTermios(int(file.Fd()), unix.TCGETS)
	return err == nil
}
