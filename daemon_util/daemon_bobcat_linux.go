// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by
// license that can be found in the LICENSE file.

//go:build linux

package daemon_util

import (
	"os"
	"os/exec"
	"strings"
	"text/template"
)

// bobCatRecord - standard record (struct) for linux openWrtRecord version of daemon package
type bobCatRecord struct {
	name           string
	description    string
	executablePath string
	template       string
}

// Standard service path for systemV daemons
func (linux *bobCatRecord) servicePath() string {
	return "/etc/init.d/S90" + linux.name
}

// Is a service installed
func (linux *bobCatRecord) isInstalled() bool {

	if _, err := os.Stat(linux.servicePath()); err == nil {
		return true
	}

	return false
}

// Check service is running
func (linux *bobCatRecord) checkRunning() (string, bool) {
	srvPath := linux.servicePath()
	output, err := exec.Command(srvPath, "status").Output()
	if err == nil && strings.Contains(string(output), "running") {
		return "Service is running...", true
	}

	return "Service is stopped", false
}

// Install the service
func (linux *bobCatRecord) Install(args ...string) (string, error) {
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
	if err := validateExecutable(execPatch); err != nil {
		return installAction + failed, err
	}

	funcs := template.FuncMap{
		"shellQuote": shellQuote,
	}
	if err := writeTemplateFile(
		srvPath,
		"bobCatConfig",
		linux.template,
		funcs,
		&struct {
			Name, Description, Path, Args string
		}{linux.name, linux.description, execPatch, shellQuoteArgs(args)},
		0755,
	); err != nil {
		return installAction + failed, err
	}

	//check restart file

	return installAction + success, nil
}

// Remove the service
func (linux *bobCatRecord) Remove() (string, error) {
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
func (linux *bobCatRecord) Start() (string, error) {
	startAction := "Starting " + linux.description + ":"

	if ok, err := checkPrivileges(); !ok {
		return startAction + failed, err
	}

	if !linux.isInstalled() {
		return startAction + failed, ErrNotInstalled
	}

	if _, ok := linux.checkRunning(); ok {
		return startAction + failed, ErrAlreadyRunning
	}

	srvPath := linux.servicePath()
	if err := exec.Command(srvPath, "start").Run(); err != nil {
		return startAction + failed, err
	}

	return startAction + success, nil
}

// Stop the service
func (linux *bobCatRecord) Stop() (string, error) {
	stopAction := "Stopping " + linux.description + ":"

	if ok, err := checkPrivileges(); !ok {
		return stopAction + failed, err
	}

	if !linux.isInstalled() {
		return stopAction + failed, ErrNotInstalled
	}

	if _, ok := linux.checkRunning(); !ok {
		return stopAction + failed, ErrAlreadyStopped
	}

	srvPath := linux.servicePath()
	if err := exec.Command(srvPath, "stop").Run(); err != nil {
		return stopAction + failed, err
	}

	return stopAction + success, nil
}

// Status - Get service status
func (linux *bobCatRecord) Status() (string, error) {

	if ok, err := checkPrivileges(); !ok {
		return "", err
	}

	if !linux.isInstalled() {
		return statNotInstalled, ErrNotInstalled
	}

	statusAction, _ := linux.checkRunning()
	return statusAction, nil
}

// Run - Run service
func (linux *bobCatRecord) Run(e Executable) (string, error) {
	runAction := "Running " + linux.description + ":"
	e.Run()
	return runAction + " completed.", nil
}

// GetTemplate - gets service config template
func (linux *bobCatRecord) GetTemplate() string {
	return linux.template
}

// SetTemplate - sets service config template
func (linux *bobCatRecord) SetTemplate(tplStr string) error {
	linux.template = tplStr
	return nil
}

const defaultBobCatConfig = `#!/bin/sh

NAME={{shellQuote .Name}}
DAEMON={{shellQuote .Path}}
PIDFILE=${PIDFILE:-/var/run/$NAME.pid}

[ -r "/etc/default/$NAME" ] && . "/etc/default/$NAME" "$1"

read_pid() {
	[ -r "$PIDFILE" ] || return 1
	pid=$(cat "$PIDFILE")
	case "$pid" in
		''|*[!0-9]*) return 1 ;;
	esac
}

is_running() {
	read_pid && kill -0 "$pid" 2>/dev/null
}

do_start() {
	echo -n "Starting $NAME: "
	if start-stop-daemon --start --quiet --background --make-pidfile \
		--pidfile "$PIDFILE" --exec "$DAEMON" -- {{.Args}}; then
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
	if start-stop-daemon --stop --quiet --pidfile "$PIDFILE" --exec "$DAEMON"; then
		retries=12
		while kill -0 "$pid" 2>/dev/null && [ "$retries" -gt 0 ]; do
			sleep 1
			retries=$((retries - 1))
		done
		if kill -0 "$pid" 2>/dev/null; then
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
