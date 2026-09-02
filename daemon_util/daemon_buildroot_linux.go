// Copyright (c) 2026 LazyEasyDev
// Licensed under the MIT License. See LICENSE in the project root.

//go:build linux

package daemon_util

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

// buildrootRecord manages services started directly from /etc/init.d.
type buildrootRecord struct {
	serviceConfig
	name           string
	description    string
	executablePath string
}

// Standard service path for Buildroot-style daemons
func (linux *buildrootRecord) servicePath() string {
	return "/etc/init.d/S90" + linux.name
}

// Is a service installed
func (linux *buildrootRecord) isInstalled() bool {

	if _, err := os.Stat(linux.servicePath()); err == nil {
		return true
	}

	return false
}

// Check service is running
func (linux *buildrootRecord) checkRunning() (string, bool, error) {
	srvPath := linux.servicePath()
	output, err := exec.Command(srvPath, "status").CombinedOutput()
	running, recognized := buildrootStatus(string(output), commandExitCode(err))
	if !recognized {
		return "", false, statusCommandError("Buildroot init", linux.name, output, err)
	}
	if running {
		return "Service is running...", true, nil
	}
	return "Service is stopped", false, nil
}

func buildrootStatus(status string, exitCode int) (running, recognized bool) {
	status = strings.TrimSpace(status)
	if exitCode == 0 && strings.Contains(status, " is running") {
		return true, true
	}
	if exitCode == 3 && strings.Contains(status, " is stopped") {
		return false, true
	}
	return false, false
}

// Install the service
func (linux *buildrootRecord) Install(args ...string) (string, error) {
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

	funcs := template.FuncMap{
		"shellQuote": shellQuote,
	}
	if err := writeTemplateFile(
		srvPath,
		"buildrootConfig",
		defaultBuildrootConfig,
		funcs,
		&struct {
			Name, Description, Path, Args, WorkingDirectory string
			StopTimeoutSeconds                              int64
		}{linux.name, linux.description, execPatch, shellQuoteArgs(args), filepath.Dir(execPatch), linux.stopTimeoutSeconds()},
		0755,
	); err != nil {
		return installAction + failed, err
	}

	return installAction + success, nil
}

// Remove the service
func (linux *buildrootRecord) Remove() (string, error) {
	removeAction := "Removing " + linux.description + ":"

	if ok, err := checkPrivileges(); !ok {
		return removeAction + failed, err
	}

	if !linux.isInstalled() {
		return removeAction + failed, ErrNotInstalled
	}

	if err := os.Remove(linux.servicePath()); err != nil {
		return removeAction + failed, err
	}

	return removeAction + success, nil
}

// Start the service
func (linux *buildrootRecord) Start() (string, error) {
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

	srvPath := linux.servicePath()
	if err := exec.Command(srvPath, "start").Run(); err != nil {
		return startAction + failed, err
	}

	return startAction + success, nil
}

// Stop the service
func (linux *buildrootRecord) Stop() (string, error) {
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

	srvPath := linux.servicePath()
	if err := exec.Command(srvPath, "stop").Run(); err != nil {
		return stopAction + failed, err
	}

	return stopAction + success, nil
}

// Status - Get service status
func (linux *buildrootRecord) Status() (string, error) {

	if ok, err := checkPrivileges(); !ok {
		return "", err
	}

	if !linux.isInstalled() {
		return statNotInstalled, ErrNotInstalled
	}

	statusAction, _, err := linux.checkRunning()
	return statusAction, err
}

const defaultBuildrootConfig = `#!/bin/sh

NAME={{shellQuote .Name}}
DAEMON={{shellQuote .Path}}
WORKING_DIRECTORY={{shellQuote .WorkingDirectory}}
PIDFILE=${PIDFILE:-/var/run/$NAME.pid}
STOP_TIMEOUT={{.StopTimeoutSeconds}}

[ -r "/etc/default/$NAME" ] && . "/etc/default/$NAME" "$1"

read_pid() {
	[ -r "$PIDFILE" ] || return 1
	pid=$(cat "$PIDFILE")
	case "$pid" in
		''|*[!0-9]*) return 1 ;;
	esac
}

is_running() {
	read_pid && start-stop-daemon -K -t -q -p "$PIDFILE" -x "$DAEMON"
}

do_start() {
	echo -n "Starting $NAME: "
	if ! cd "$WORKING_DIRECTORY"; then
		echo "FAIL"
		return 1
	fi
	if start-stop-daemon -S -q -b -m \
		-p "$PIDFILE" -x "$DAEMON" -- {{.Args}}; then
		sleep 1
		if is_running; then
			echo "OK"
		else
			rm -f "$PIDFILE"
			echo "FAIL"
			return 1
		fi
	else
		echo "FAIL"
		return 1
	fi
}

do_stop() {
	echo -n "Stopping $NAME: "
	if ! read_pid; then
		echo "FAIL"
		return 1
	fi
	if start-stop-daemon -K -q -p "$PIDFILE" -x "$DAEMON"; then
		elapsed=0
		while is_running && [ "$elapsed" -lt "$STOP_TIMEOUT" ]; do
			sleep 1
			elapsed=$((elapsed + 1))
		done
		if is_running; then
			if ! start-stop-daemon -K -q -s KILL -p "$PIDFILE" -x "$DAEMON"; then
				echo "FAIL"
				return 1
			fi
		fi
		force_elapsed=0
		while is_running && [ "$force_elapsed" -lt 5 ]; do
			sleep 1
			force_elapsed=$((force_elapsed + 1))
		done
		if is_running; then
			echo "FAIL"
			return 1
		fi
		rm -f "$PIDFILE"
		echo "OK"
	else
		echo "FAIL"
		return 1
	fi
}

do_status() {
	if is_running; then
		echo "$NAME is running (pid $pid)"
		return 0
	fi
	rm -f "$PIDFILE"
	echo "$NAME is stopped"
	return 3
}

case "$1" in
	start)
		do_start
		;;
	stop)
		do_stop
		;;
	restart)
		do_stop &&
			sleep 12 &&
			do_start
		;;
	status)
		do_status
		;;
	*)
		echo "Usage: $0 {start|stop|restart|status}"
		exit 1
esac
`
