// Copyright (c) 2026 LazyEasyDev
// Licensed under the MIT License. See LICENSE in the project root.

//go:build darwin

// Package daemon darwin (mac os x) version
package daemon_util

import (
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"text/template"
	"time"
)

const (
	launchdStopPollInterval = 50 * time.Millisecond
	launchdStopWaitMargin   = 5 * time.Second
)

// darwinRecord - standard record (struct) for darwin version of daemon package
type darwinRecord struct {
	serviceConfig
	name           string
	description    string
	kind           Kind
	executablePath string
	path           string
}

func newDaemon(name, description string, kind Kind, executablePath string) (Daemon, error) {
	var path string
	switch kind {
	case UserAgent:
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, "Library", "LaunchAgents", name+".plist")
	case GlobalAgent:
		path = filepath.Join("/Library/LaunchAgents", name+".plist")
	case GlobalDaemon:
		path = filepath.Join("/Library/LaunchDaemons", name+".plist")
	}

	return &darwinRecord{
		name:           name,
		description:    description,
		kind:           kind,
		executablePath: executablePath,
		path:           path,
	}, nil
}

// ListServiceStatuses returns the status of services registered by this tool.
func ListServiceStatuses() ([]ServiceStatus, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	statusDirectory := func(path string, kind Kind) serviceDirectory {
		return serviceDirectory{
			path:   path,
			suffix: ".plist",
			isRunning: func(name string) (bool, error) {
				_, running, err := (&darwinRecord{name: name, kind: kind}).checkRunning()
				return running, err
			},
		}
	}
	return listServiceStatuses(
		statusDirectory(filepath.Join(home, "Library", "LaunchAgents"), UserAgent),
		statusDirectory("/Library/LaunchAgents", GlobalAgent),
		statusDirectory("/Library/LaunchDaemons", GlobalDaemon),
	)
}

// Standard service path for system daemons
func (darwin *darwinRecord) servicePath() string {
	return darwin.path
}

// Is a service installed
func (darwin *darwinRecord) isInstalled() bool {

	if _, err := os.Stat(darwin.servicePath()); err == nil {
		return true
	}

	return false
}

func (darwin *darwinRecord) launchDomain() (string, error) {
	switch darwin.kind {
	case UserAgent:
		return fmt.Sprintf("gui/%d", os.Getuid()), nil
	case GlobalAgent:
		info, err := os.Stat("/dev/console")
		if err != nil {
			return "", err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid == 0 {
			return "", errors.New("no logged-in GUI user")
		}
		return fmt.Sprintf("gui/%d", stat.Uid), nil
	case GlobalDaemon:
		return "system", nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidKind, darwin.kind)
	}
}

func (darwin *darwinRecord) launchTarget() (string, error) {
	domain, err := darwin.launchDomain()
	if err != nil {
		return "", err
	}
	return domain + "/" + darwin.name, nil
}

// Check service is running
func (darwin *darwinRecord) checkRunning() (string, bool, error) {
	target, err := darwin.launchTarget()
	if err != nil {
		return "", false, fmt.Errorf("resolve launchd target: %w", err)
	}
	output, err := exec.Command("launchctl", "print", target).CombinedOutput()
	running, recognized := launchdStatus(string(output), commandExitCode(err))
	if !recognized {
		return "", false, statusCommandError("launchd", darwin.name, output, err)
	}
	if running {
		reg := regexp.MustCompile(`(?m)\bpid = ([0-9]+)\b`)
		data := reg.FindStringSubmatch(string(output))
		if len(data) > 1 {
			return "Service (pid  " + data[1] + ") is running...", true, nil
		}
		return "Service is loaded...", true, nil
	}
	return "Service is stopped", false, nil
}

func launchdStatus(status string, exitCode int) (running, recognized bool) {
	if exitCode == 0 {
		return true, true
	}
	if exitCode == 113 && strings.Contains(status, "Could not find service") {
		return false, true
	}
	return false, false
}

func waitForLaunchdStop(timeout, pollInterval time.Duration, running func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		isRunning, err := running()
		if err != nil {
			return err
		}
		if !isRunning {
			return nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return errors.New("timed out waiting for launchd service to stop")
		}
		time.Sleep(min(pollInterval, remaining))
	}
}

// Install the service
func (darwin *darwinRecord) Install(args ...string) (string, error) {
	installAction := "Install " + darwin.description + ":"

	ok, err := checkPrivileges()
	if !ok && darwin.kind != UserAgent {
		return installAction + failed, err
	}

	srvPath := darwin.servicePath()

	if darwin.isInstalled() {
		return installAction + failed, ErrAlreadyInstalled
	}
	if err := os.MkdirAll(filepath.Dir(srvPath), 0755); err != nil {
		return installAction + failed, err
	}

	execPatch, err := resolveExecutablePath(darwin.name, darwin.executablePath)
	if err != nil {
		return installAction + failed, err
	}

	funcs := template.FuncMap{
		"xml": html.EscapeString,
	}
	if err := writeTemplateFile(
		srvPath,
		"propertyList",
		defaultPropertyList,
		funcs,
		&struct {
			Name, Path, WorkingDirectory string
			Args                         []string
			StopTimeoutSeconds           int64
		}{darwin.name, execPatch, filepath.Dir(execPatch), args, darwin.stopTimeoutSeconds()},
		0644,
	); err != nil {
		return installAction + failed, err
	}

	return installAction + success, nil
}

// Remove the service
func (darwin *darwinRecord) Remove() (string, error) {
	removeAction := "Removing " + darwin.description + ":"

	ok, err := checkPrivileges()
	if !ok && darwin.kind != UserAgent {
		return removeAction + failed, err
	}

	if !darwin.isInstalled() {
		return removeAction + failed, ErrNotInstalled
	}

	if err := os.Remove(darwin.servicePath()); err != nil {
		return removeAction + failed, err
	}

	return removeAction + success, nil
}

// Start the service
func (darwin *darwinRecord) Start() (string, error) {
	startAction := "Starting " + darwin.description + ":"

	ok, err := checkPrivileges()
	if !ok && darwin.kind != UserAgent {
		return startAction + failed, err
	}

	if !darwin.isInstalled() {
		return startAction + failed, ErrNotInstalled
	}

	if _, running, err := darwin.checkRunning(); err != nil {
		return startAction + failed, err
	} else if running {
		return startAction + failed, ErrAlreadyRunning
	}

	domain, err := darwin.launchDomain()
	if err != nil {
		return startAction + failed, err
	}
	if err := exec.Command("launchctl", "bootstrap", domain, darwin.servicePath()).Run(); err != nil {
		return startAction + failed, err
	}

	return startAction + success, nil
}

// Stop the service
func (darwin *darwinRecord) Stop() (string, error) {
	stopAction := "Stopping " + darwin.description + ":"

	ok, err := checkPrivileges()
	if !ok && darwin.kind != UserAgent {
		return stopAction + failed, err
	}

	if !darwin.isInstalled() {
		return stopAction + failed, ErrNotInstalled
	}

	if _, running, err := darwin.checkRunning(); err != nil {
		return stopAction + failed, err
	} else if !running {
		return stopAction + failed, ErrAlreadyStopped
	}

	target, err := darwin.launchTarget()
	if err != nil {
		return stopAction + failed, err
	}
	if err := exec.Command("launchctl", "bootout", target).Run(); err != nil {
		return stopAction + failed, err
	}
	waitTimeout := darwin.stopTimeoutDuration()
	if waitTimeout < time.Duration(1<<63-1)-launchdStopWaitMargin {
		waitTimeout += launchdStopWaitMargin
	}
	if err := waitForLaunchdStop(waitTimeout, launchdStopPollInterval, func() (bool, error) {
		_, running, err := darwin.checkRunning()
		return running, err
	}); err != nil {
		return stopAction + failed, err
	}

	return stopAction + success, nil
}

// Status - Get service status
func (darwin *darwinRecord) Status() (string, error) {

	ok, err := checkPrivileges()
	if !ok && darwin.kind != UserAgent {
		return "", err
	}

	if !darwin.isInstalled() {
		return statNotInstalled, ErrNotInstalled
	}

	statusAction, _, err := darwin.checkRunning()
	return statusAction, err
}

const defaultPropertyList = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>KeepAlive</key>
	<true/>
	<key>ExitTimeOut</key>
	<integer>{{.StopTimeoutSeconds}}</integer>
	<key>Label</key>
	<string>{{xml .Name}}</string>
	<key>ProgramArguments</key>
	<array>
	    <string>{{xml .Path}}</string>
		{{range .Args}}<string>{{xml .}}</string>
		{{end}}
	</array>
	<key>RunAtLoad</key>
	<true/>
    <key>WorkingDirectory</key>
	<string>{{xml .WorkingDirectory}}</string>
</dict>
</plist>
`
