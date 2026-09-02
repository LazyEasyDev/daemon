//go:build linux

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/LazyEasyDev/daemon/daemon_util"
	"github.com/urfave/cli/v3"
	"golang.org/x/sys/unix"
)

func configuredInstallService(command *cli.Command, serviceName, executablePath string) (daemon_util.Daemon, []string, error) {
	service, err := configuredService(command, serviceName, executablePath)
	if err == nil {
		if warning := selinuxInstallWarning(executablePath, "/sys/fs/selinux/enforce", readSELinuxFileContext); warning != "" {
			_, _ = fmt.Fprintln(os.Stderr, warning)
		}
	}
	return service, nil, err
}

func selinuxInstallWarning(executablePath, enforcePath string, readFileContext func(string) (string, error)) string {
	enforcing, err := os.ReadFile(enforcePath)
	if err != nil || strings.TrimSpace(string(enforcing)) != "1" {
		return ""
	}

	context, err := readFileContext(executablePath)
	if err != nil {
		return ""
	}
	fields := strings.Split(strings.TrimRight(context, "\x00"), ":")
	if len(fields) < 3 || fields[2] != "user_home_t" {
		return ""
	}

	return fmt.Sprintf("Warning: SELinux is enforcing and may prevent the system service from executing %q; move the application to a trusted system path such as /opt or configure a persistent SELinux file context.", executablePath)
}

func readSELinuxFileContext(path string) (string, error) {
	size, err := unix.Getxattr(path, "security.selinux", nil)
	if err != nil {
		return "", err
	}
	context := make([]byte, size)
	size, err = unix.Getxattr(path, "security.selinux", context)
	if err != nil {
		return "", err
	}
	return string(context[:size]), nil
}

func platformCommands() []*cli.Command {
	return nil
}

func isTerminal(file *os.File) bool {
	_, err := unix.IoctlGetTermios(int(file.Fd()), unix.TCGETS)
	return err == nil
}
