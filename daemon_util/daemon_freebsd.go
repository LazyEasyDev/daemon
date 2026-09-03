// Copyright (c) 2026 LazyEasyDev
// Licensed under the MIT License. See LICENSE in the project root.

//go:build freebsd

package daemon_util

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

// systemVRecord - standard record (struct) for linux systemV version of daemon package
type bsdRecord struct {
	serviceConfig
	name           string
	description    string
	executablePath string
}

// Standard service path for systemV daemons
func (bsd *bsdRecord) servicePath() string {
	return "/usr/local/etc/rc.d/" + bsd.name
}

// Is a service installed
func (bsd *bsdRecord) isInstalled() bool {

	if _, err := os.Stat(bsd.servicePath()); err == nil {
		return true
	}

	return false
}

// Is a service is enabled
func (bsd *bsdRecord) isEnabled() (bool, error) {
	err := exec.Command("service", bsd.name, "enabled").Run()
	if err == nil {
		return true, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return false, nil
	}
	return false, err
}

func (bsd *bsdRecord) getCmd(cmd string) (string, error) {
	enabled, err := bsd.isEnabled()
	if err != nil {
		return "", err
	}
	if !enabled {
		return "one" + cmd, nil
	}
	return cmd, nil
}

// Get the daemon properly
func newDaemon(name, description string, _ Kind, executablePath string) (Daemon, error) {
	return &bsdRecord{
		name:           name,
		description:    description,
		executablePath: executablePath,
	}, nil
}

// ListServiceStatuses returns the status of services registered by this tool.
func ListServiceStatuses() ([]ServiceStatus, error) {
	return listServiceStatuses(serviceDirectory{
		path: "/usr/local/etc/rc.d",
		isRunning: func(name string) (bool, error) {
			_, running, err := (&bsdRecord{name: name}).checkRunning()
			return running, err
		},
	})
}

// Check service is running
func (bsd *bsdRecord) checkRunning() (string, bool, error) {
	command, err := bsd.getCmd("status")
	if err != nil {
		return "", false, statusCommandError("FreeBSD rc.d", bsd.name, nil, err)
	}
	output, err := exec.Command("service", bsd.name, command).CombinedOutput()
	running, recognized := freeBSDStatus(string(output), commandExitCode(err))
	if !recognized {
		return "", false, statusCommandError("FreeBSD rc.d", bsd.name, output, err)
	}
	if running {
		if pid := freeBSDStatusPID(string(output)); pid != "" {
			return "Service (pid  " + pid + ") is running...", true, nil
		}
		return "Service is running...", true, nil
	}
	return "Service is stopped", false, nil
}

func freeBSDStatusPID(status string) string {
	data := regexp.MustCompile(`pid[[:space:]]+([0-9]+)`).FindStringSubmatch(status)
	if len(data) > 1 {
		return data[1]
	}
	return ""
}

func freeBSDStatus(status string, exitCode int) (running, recognized bool) {
	if exitCode == 0 {
		return true, true
	}
	if exitCode == 1 && strings.Contains(strings.ToLower(status), "is not running") {
		return false, true
	}
	return false, false
}

// Install the service
func (bsd *bsdRecord) Install(args ...string) (string, error) {
	installAction := "Install " + bsd.description + ":"

	if ok, err := checkPrivileges(); !ok {
		return installAction + failed, err
	}

	srvPath := bsd.servicePath()

	if bsd.isInstalled() {
		return installAction + failed, ErrAlreadyInstalled
	}

	execPatch, err := resolveExecutablePath(bsd.name, bsd.executablePath)
	if err != nil {
		return installAction + failed, err
	}

	funcs := template.FuncMap{
		"shellQuote": shellQuote,
	}
	if err := writeTemplateFile(
		srvPath,
		"bsdConfig",
		defaultBSDConfig,
		funcs,
		&struct {
			Name, RCName, RCVar, Description, Path, Args, WorkingDirectory string
			StopTimeoutSeconds                                             int64
		}{bsd.name, bsd.name, bsd.name + "_enable", bsd.description, execPatch, shellQuoteArgs(args), filepath.Dir(execPatch), bsd.stopTimeoutSeconds()},
		0755,
	); err != nil {
		return installAction + failed, err
	}
	if err := exec.Command("service", bsd.name, "enable").Run(); err != nil {
		if removeErr := os.Remove(srvPath); removeErr != nil {
			return installAction + failed, errors.Join(err, removeErr)
		}
		return installAction + failed, err
	}

	return installAction + success, nil
}

// Remove the service
func (bsd *bsdRecord) Remove() (string, error) {
	removeAction := "Removing " + bsd.description + ":"

	if ok, err := checkPrivileges(); !ok {
		return removeAction + failed, err
	}

	if !bsd.isInstalled() {
		return removeAction + failed, ErrNotInstalled
	}

	if err := exec.Command("service", bsd.name, "disable").Run(); err != nil {
		return removeAction + failed, err
	}
	if err := os.Remove(bsd.servicePath()); err != nil {
		return removeAction + failed, err
	}

	return removeAction + success, nil
}

// Start the service
func (bsd *bsdRecord) Start() (string, error) {
	startAction := "Starting " + bsd.description + ":"

	if ok, err := checkPrivileges(); !ok {
		return startAction + failed, err
	}

	if !bsd.isInstalled() {
		return startAction + failed, ErrNotInstalled
	}

	if _, running, err := bsd.checkRunning(); err != nil {
		return startAction + failed, err
	} else if running {
		return startAction + failed, ErrAlreadyRunning
	}

	command, err := bsd.getCmd("start")
	if err != nil {
		return startAction + failed, err
	}
	if err := exec.Command("service", bsd.name, command).Run(); err != nil {
		return startAction + failed, err
	}

	return startAction + success, nil
}

// Stop the service
func (bsd *bsdRecord) Stop() (string, error) {
	stopAction := "Stopping " + bsd.description + ":"

	if ok, err := checkPrivileges(); !ok {
		return stopAction + failed, err
	}

	if !bsd.isInstalled() {
		return stopAction + failed, ErrNotInstalled
	}

	if _, running, err := bsd.checkRunning(); err != nil {
		return stopAction + failed, err
	} else if !running {
		return stopAction + failed, ErrAlreadyStopped
	}

	command, err := bsd.getCmd("stop")
	if err != nil {
		return stopAction + failed, err
	}
	if err := exec.Command("service", bsd.name, command).Run(); err != nil {
		return stopAction + failed, err
	}

	return stopAction + success, nil
}

// Status - Get service status
func (bsd *bsdRecord) Status() (string, error) {

	if ok, err := checkPrivileges(); !ok {
		return "", err
	}

	if !bsd.isInstalled() {
		return statNotInstalled, ErrNotInstalled
	}

	statusAction, _, err := bsd.checkRunning()
	return statusAction, err
}

const defaultBSDConfig = `#!/bin/sh
#
# PROVIDE: {{.RCName}}
# REQUIRE: NETWORKING syslogd
# KEYWORD:

# Boot startup is managed with service {{.Name}} enable/disable.


. /etc/rc.subr

name={{shellQuote .RCName}}
rcvar={{shellQuote .RCVar}}
command="/usr/sbin/daemon"
app_command={{shellQuote .Path}}
app_directory={{shellQuote .WorkingDirectory}}
pidfile="/var/run/$name.pid"
child_pidfile="/var/run/$name.child.pid"
stop_timeout={{.StopTimeoutSeconds}}

start_cmd="daemon_start"
stop_cmd="daemon_stop"
status_cmd="daemon_status"
daemon_start()
{
	cd "$app_directory" || return 1
	"$command" -R 30 -P "$pidfile" -p "$child_pidfile" -f "$app_command" {{.Args}}
}
daemon_supervisor_pid()
{
	/usr/bin/pgrep -L -F "$pidfile" 2>/dev/null
}
daemon_status()
{
	supervisor_pid=$(daemon_supervisor_pid)
	if [ -n "$supervisor_pid" ]; then
		echo "$name is running as pid $supervisor_pid."
		return 0
	fi
	echo "$name is not running."
	return 1
}
daemon_stop()
{
	supervisor_pid=$(daemon_supervisor_pid)
	[ -n "$supervisor_pid" ] || return 1
	kill -TERM "$supervisor_pid" 2>/dev/null || return 1

	elapsed=0
	while kill -0 "$supervisor_pid" 2>/dev/null; do
		if [ "$elapsed" -ge "$stop_timeout" ]; then
			child_pid=$(check_pidfile "$child_pidfile" "$app_command")
			if [ -n "$child_pid" ] && ! kill -KILL "$child_pid" 2>/dev/null; then
				return 1
			fi
			if kill -0 "$supervisor_pid" 2>/dev/null && ! kill -KILL "$supervisor_pid" 2>/dev/null; then
				return 1
			fi

			force_elapsed=0
			while [ "$force_elapsed" -lt 5 ]; do
				supervisor_running=false
				kill -0 "$supervisor_pid" 2>/dev/null && supervisor_running=true
				child_pid=$(check_pidfile "$child_pidfile" "$app_command")
				if ! $supervisor_running && [ -z "$child_pid" ]; then
					break
				fi
				sleep 1
				force_elapsed=$((force_elapsed + 1))
			done
			child_pid=$(check_pidfile "$child_pidfile" "$app_command")
			if kill -0 "$supervisor_pid" 2>/dev/null || [ -n "$child_pid" ]; then
				return 1
			fi
			break
		fi
		sleep 1
		elapsed=$((elapsed + 1))
	done
	rm -f "$pidfile" "$child_pidfile"
}
load_rc_config $name
run_rc_command "$1"
`
