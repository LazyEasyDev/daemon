//go:build !windows

package main

import (
	"github.com/LazyEasyDev/daemon/daemon_util"
	"github.com/urfave/cli/v3"
)

func configuredInstallService(command *cli.Command, serviceName, executablePath string) (daemon_util.Daemon, []string, error) {
	service, err := configuredService(command, serviceName, executablePath)
	return service, nil, err
}

func platformCommands() []*cli.Command {
	return nil
}
