// Copyright (c) 2026 LazyEasyDev
// Licensed under the MIT License. See LICENSE in the project root.

//go:build linux

package daemon_util

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

// systemVRecord - standard record (struct) for linux systemV version of daemon package
type systemVRecord struct {
	serviceConfig
	name           string
	description    string
	executablePath string
}

// Standard service path for systemV daemons
func (linux *systemVRecord) servicePath() string {
	return "/etc/init.d/" + linux.name
}

// Is a service installed
func (linux *systemVRecord) isInstalled() bool {

	if _, err := os.Stat(linux.servicePath()); err == nil {
		return true
	}

	return false
}

// Check service is running
func (linux *systemVRecord) checkRunning() (string, bool, error) {
	output, err := exec.Command("service", linux.name, "status").CombinedOutput()
	running, recognized := systemVStatus(linux.name, string(output), commandExitCode(err))
	if !recognized {
		return "", false, statusCommandError("System V", linux.name, output, err)
	}
	if running {
		reg := regexp.MustCompile("pid  ([0-9]+)")
		data := reg.FindStringSubmatch(string(output))
		if len(data) > 1 {
			return "Service (pid  " + data[1] + ") is running...", true, nil
		}
		return "Service is running...", true, nil
	}
	return "Service is stopped", false, nil
}

func systemVStatus(name, status string, exitCode int) (running, recognized bool) {
	status = strings.TrimSpace(status)
	if exitCode == 0 && strings.Contains(status, name) && strings.Contains(status, " is running") {
		return true, true
	}
	if exitCode == 3 && strings.Contains(status, name) && strings.Contains(status, " is stopped") {
		return false, true
	}
	return false, false
}

// Install the service
func (linux *systemVRecord) Install(args ...string) (string, error) {
	installAction := "Install " + linux.description + ":"

	if ok, err := checkPrivileges(); !ok {
		return installAction + failed, err
	}

	srvPath := linux.servicePath()

	if linux.isInstalled() {
		return installAction + failed, ErrAlreadyInstalled
	}

	execPatch, err := resolveExecutablePath(linux.name, linux.executablePath)
	if err != nil {
		return installAction + failed, err
	}
	links, hasStartLink := linux.existingServiceLinks()
	if !hasStartLink {
		return installAction + failed, fmt.Errorf("%w: no System V start runlevel directories found", ErrUnsupportedSystem)
	}

	funcs := template.FuncMap{
		"shellQuote": shellQuote,
	}
	if err := writeTemplateFile(
		srvPath,
		"systemVConfig",
		defaultSystemVConfig,
		funcs,
		&struct {
			Name, Description, Path, Args, WorkingDirectory string
			StopTimeoutSeconds                              int64
		}{linux.name, linux.description, execPatch, shellQuoteArgs(args), filepath.Dir(execPatch), linux.stopTimeoutSeconds()},
		0755,
	); err != nil {
		return installAction + failed, err
	}

	if err := createServiceLinks(srvPath, links); err != nil {
		_ = os.Remove(srvPath)
		return installAction + failed, err
	}

	return installAction + success, nil
}

// Remove the service
func (linux *systemVRecord) Remove() (string, error) {
	removeAction := "Removing " + linux.description + ":"

	if ok, err := checkPrivileges(); !ok {
		return removeAction + failed, err
	}

	if !linux.isInstalled() {
		return removeAction + failed, ErrNotInstalled
	}

	var removeErrors []error
	for _, path := range append(linux.serviceLinks(), linux.servicePath()) {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			removeErrors = append(removeErrors, err)
		}
	}
	if err := errors.Join(removeErrors...); err != nil {
		return removeAction + failed, err
	}

	return removeAction + success, nil
}

// Start the service
func (linux *systemVRecord) Start() (string, error) {
	startAction := "Starting " + linux.description + ":"

	if ok, err := checkPrivileges(); !ok {
		return startAction + failed, err
	}

	if !linux.isInstalled() {
		return startAction + failed, ErrNotInstalled
	}

	if _, running, err := linux.checkRunning(); err != nil {
		return startAction + failed, err
	} else if running {
		return startAction + failed, ErrAlreadyRunning
	}

	if err := exec.Command("service", linux.name, "start").Run(); err != nil {
		return startAction + failed, err
	}

	return startAction + success, nil
}

// Stop the service
func (linux *systemVRecord) Stop() (string, error) {
	stopAction := "Stopping " + linux.description + ":"

	if ok, err := checkPrivileges(); !ok {
		return stopAction + failed, err
	}

	if !linux.isInstalled() {
		return stopAction + failed, ErrNotInstalled
	}

	if _, running, err := linux.checkRunning(); err != nil {
		return stopAction + failed, err
	} else if !running {
		return stopAction + failed, ErrAlreadyStopped
	}

	if err := exec.Command("service", linux.name, "stop").Run(); err != nil {
		return stopAction + failed, err
	}

	return stopAction + success, nil
}

// Status - Get service status
func (linux *systemVRecord) Status() (string, error) {

	if ok, err := checkPrivileges(); !ok {
		return "", err
	}

	if !linux.isInstalled() {
		return statNotInstalled, ErrNotInstalled
	}

	statusAction, _, err := linux.checkRunning()
	return statusAction, err
}

func (linux *systemVRecord) serviceLinks() []string {
	return systemVServiceLinks("/", linux.name)
}

func (linux *systemVRecord) existingServiceLinks() ([]string, bool) {
	return existingSystemVServiceLinks("/", linux.name)
}

func systemVServiceLinks(root, name string) []string {
	links := make([]string, 0, 7)
	for _, runlevel := range [...]string{"2", "3", "4", "5"} {
		directory := filepath.Join(root, "etc", "rc"+runlevel+".d")
		links = append(links, filepath.Join(directory, "S87"+name))
	}
	for _, runlevel := range [...]string{"0", "1", "6"} {
		directory := filepath.Join(root, "etc", "rc"+runlevel+".d")
		links = append(links, filepath.Join(directory, "K17"+name))
	}
	return links
}

func existingSystemVServiceLinks(root, name string) ([]string, bool) {
	links := make([]string, 0, 7)
	hasStartLink := false
	for _, link := range systemVServiceLinks(root, name) {
		if info, err := os.Stat(filepath.Dir(link)); err == nil && info.IsDir() {
			links = append(links, link)
			if strings.HasPrefix(filepath.Base(link), "S") {
				hasStartLink = true
			}
		}
	}
	return links, hasStartLink
}

const defaultSystemVConfig = `#! /bin/sh
#
#       /etc/rc.d/init.d/{{.Name}}
#
#       Starts {{.Name}} as a daemon
#
# chkconfig: 2345 87 17
# description: Starts and stops a single {{.Name}} instance on this system

### BEGIN INIT INFO
# Provides: {{.Name}} 
# Required-Start: $network $named
# Required-Stop: $network $named
# Default-Start: 2 3 4 5
# Default-Stop: 0 1 6
# Short-Description: This service manages the {{.Description}}.
# Description: {{.Description}}
### END INIT INFO

exec={{shellQuote .Path}}
servname={{shellQuote .Description}}
working_directory={{shellQuote .WorkingDirectory}}

proc="{{.Name}}"
pidfile="/var/run/$proc.pid"
lockfile="/var/lock/subsys/$proc"
stop_timeout={{.StopTimeoutSeconds}}

[ -d "$(dirname "$lockfile")" ] || mkdir -p "$(dirname "$lockfile")"

[ -e /etc/sysconfig/$proc ] && . /etc/sysconfig/$proc

read_pid() {
	[ -r "$pidfile" ] || return 1
	pid=$(cat "$pidfile")
	case "$pid" in
		''|*[!0-9]*) return 1 ;;
	esac
	[ "$pid" -gt 1 ]
}

is_expected_process() {
	read_pid || return 1
	[ "$exec" -ef "/proc/$pid/exe" ]
}

is_process_group_running() {
	read_pid || return 1
	kill -0 -- "-$pid" 2>/dev/null
}

start() {
	[ -x "$exec" ] || exit 5
	command -v setsid >/dev/null 2>&1 || exit 5
	cd "$working_directory" || exit 5

	if [ -f "$pidfile" ]; then
		if read_pid && ! is_expected_process && ! kill -0 "$pid" 2>/dev/null; then
			rm -f "$pidfile"
			if [ -f "$lockfile" ]; then
				rm -f "$lockfile"
			fi
		fi
	fi

	if ! [ -f "$pidfile" ]; then
		printf 'Starting %s:\t' "$servname"
		setsid "$exec" {{.Args}} >/dev/null 2>&1 &
		pid=$!
		printf '%s\n' "$pid" > "$pidfile"
		sleep 1
		if is_expected_process; then
			touch "$lockfile"
			echo "OK"
		else
			wait "$pid"
			retval=$?
			[ "$retval" -ne 0 ] || retval=1
			rm -f "$pidfile" "$lockfile"
			echo "FAIL"
			return "$retval"
		fi
	else
		# failure
		echo
		printf '%s still exists...\n' "$pidfile"
		exit 7
	fi
}

stop() {
	printf 'Stopping %s:\t' "$servname"
	if ! read_pid; then
		echo "FAIL"
		return 1
	fi
	if ! kill -0 "$pid" 2>/dev/null; then
		rm -f "$pidfile" "$lockfile"
		echo "OK"
		return 0
	fi
	if ! is_expected_process; then
		echo "FAIL"
		return 1
	fi
	if ! kill -TERM -- "-$pid" 2>/dev/null; then
		echo "FAIL"
		return 1
	fi

	elapsed=0
	while is_process_group_running && [ "$elapsed" -lt "$stop_timeout" ]; do
		sleep 1
		elapsed=$((elapsed + 1))
	done
	if is_process_group_running && ! kill -KILL -- "-$pid" 2>/dev/null; then
		echo "FAIL"
		return 1
	fi
	force_elapsed=0
	while is_process_group_running && [ "$force_elapsed" -lt 5 ]; do
		sleep 1
		force_elapsed=$((force_elapsed + 1))
	done
	if is_process_group_running; then
		echo "FAIL"
		return 1
	fi
	rm -f "$pidfile" "$lockfile"
	echo "OK"
	return 0
}

restart() {
	stop && start
}

service_status() {
	if is_expected_process; then
		printf '%s (pid  %s) is running...\n' "$proc" "$pid"
		return 0
	fi
	printf '%s is stopped\n' "$proc"
	return 3
}

case "$1" in
	start)
		service_status >/dev/null 2>&1 && exit 0
		start
		;;
	stop)
		service_status >/dev/null 2>&1 || exit 0
		stop
		;;
	restart)
		restart
		;;
	status)
		service_status
		;;
	*)
		printf 'Usage: %s {start|stop|status|restart}\n' "$0"
		exit 2
esac

exit $?
`
