//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/LazyEasyDev/daemon/daemon_util"
	"github.com/urfave/cli/v3"
	"golang.org/x/sys/unix"
)

func configuredInstallService(command *cli.Command, serviceName, executablePath string) (daemon_util.Daemon, []string, error) {
	service, err := configuredService(command, serviceName, executablePath)
	if err == nil && !command.Bool("ignore-warnings") {
		if warning := selinuxInstallWarning(executablePath, "/sys/fs/selinux/enforce", readSELinuxFileContext); warning != "" {
			interactive, terminalErr := installWarningTerminal(os.Stdin, os.Stderr)
			if terminalErr == nil {
				err = confirmInstallWarning(os.Stdin, os.Stderr, warning, false, interactive)
			}
		}
	}
	return service, nil, err
}

func selinuxInstallWarning(executablePath, enforcePath string, readFileContext func(string) (string, error)) string {
	enforcing, err := os.ReadFile(enforcePath)
	if err != nil || strings.TrimSpace(string(enforcing)) != "1" {
		return ""
	}

	context, contextErr := readFileContext(executablePath)
	context = strings.TrimRight(context, "\x00")
	contextType := ""
	validContext := false
	if contextErr == nil {
		contextType, validContext = selinuxContextType(context)
	}
	if !riskySELinuxExecutableType(contextType) && !riskySELinuxExecutablePath(executablePath) {
		return ""
	}

	contextDescription := ""
	if validContext {
		contextDescription = fmt.Sprintf("\n  Context: %q", context)
	}
	return fmt.Sprintf("Warning: SELinux may prevent this system service from starting.\n  Executable: %q%s\nSuggestion:\n  Deploy the application bundle under a root-owned path, such as:\n    /opt/<application>\n  Configure a persistent SELinux file context for the executable.\n  Moving files alone may preserve the current label.", executablePath, contextDescription)
}

func selinuxContextType(context string) (string, bool) {
	fields := strings.SplitN(context, ":", 4)
	if len(fields) < 3 || fields[0] == "" || fields[1] == "" || fields[2] == "" {
		return "", false
	}
	return fields[2], true
}

func riskySELinuxExecutableType(contextType string) bool {
	switch contextType {
	case "", "bin_t", "sbin_t", "usr_t":
		return false
	default:
		return !strings.HasSuffix(contextType, "_exec_t")
	}
}

func riskySELinuxExecutablePath(path string) bool {
	for _, root := range []string{"/home", "/var/home", "/root", "/tmp", "/var/tmp", "/run", "/dev/shm"} {
		relative, err := filepath.Rel(root, filepath.Clean(path))
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return true
		}
	}
	return false
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

func installWarningTerminal(input, output *os.File) (bool, error) {
	inputTerminal, err := terminalStatus(input)
	if err != nil {
		return false, err
	}
	outputTerminal, err := terminalStatus(output)
	if err != nil {
		return false, err
	}
	return inputTerminal && outputTerminal, nil
}

func terminalStatus(file *os.File) (bool, error) {
	_, err := unix.IoctlGetTermios(int(file.Fd()), unix.TCGETS)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, unix.ENOTTY) {
		return false, nil
	}
	return false, err
}

func isTerminal(file *os.File) bool {
	terminal, _ := terminalStatus(file)
	return terminal
}
