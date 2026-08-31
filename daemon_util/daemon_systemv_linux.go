// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by
// license that can be found in the LICENSE file.

//go:build linux

package daemon_util

import (
	"errors"
	"os"
	"os/exec"
	"regexp"
	"text/template"
)

// systemVRecord - standard record (struct) for linux systemV version of daemon package
type systemVRecord struct {
	serviceConfig
	name           string
	description    string
	executablePath string
	template       string
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
func (linux *systemVRecord) checkRunning() (string, bool) {
	output, err := exec.Command("service", linux.name, "status").Output()
	if err == nil {
		if matched, err := regexp.MatchString(linux.name, string(output)); err == nil && matched {
			reg := regexp.MustCompile("pid  ([0-9]+)")
			data := reg.FindStringSubmatch(string(output))
			if len(data) > 1 {
				return "Service (pid  " + data[1] + ") is running...", true
			}
			return "Service is running...", true
		}
	}

	return "Service is stopped", false
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

	funcs := template.FuncMap{
		"shellQuote": shellQuote,
	}
	if err := writeTemplateFile(
		srvPath,
		"systemVConfig",
		linux.template,
		funcs,
		&struct {
			Name, Description, Path, Args string
			StopTimeoutSeconds            int64
		}{linux.name, linux.description, execPatch, shellQuoteArgs(args), linux.stopTimeoutSeconds()},
		0755,
	); err != nil {
		return installAction + failed, err
	}

	if err := createServiceLinks(srvPath, linux.serviceLinks()); err != nil {
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

	if _, ok := linux.checkRunning(); ok {
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

	if _, ok := linux.checkRunning(); !ok {
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

	statusAction, _ := linux.checkRunning()

	return statusAction, nil
}

// Run - Run service
func (linux *systemVRecord) Run(e Executable) (string, error) {
	runAction := "Running " + linux.description + ":"
	e.Run()
	return runAction + " completed.", nil
}

// GetTemplate - gets service config template
func (linux *systemVRecord) GetTemplate() string {
	return linux.template
}

// SetTemplate - sets service config template
func (linux *systemVRecord) SetTemplate(tplStr string) error {
	linux.template = tplStr
	return nil
}

func (linux *systemVRecord) serviceLinks() []string {
	links := make([]string, 0, 7)
	for _, runlevel := range [...]string{"2", "3", "4", "5"} {
		links = append(links, "/etc/rc"+runlevel+".d/S87"+linux.name)
	}
	for _, runlevel := range [...]string{"0", "1", "6"} {
		links = append(links, "/etc/rc"+runlevel+".d/K17"+linux.name)
	}
	return links
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

#
# Source function library.
#
if [ -f /etc/rc.d/init.d/functions ]; then
    . /etc/rc.d/init.d/functions
fi

exec={{shellQuote .Path}}
servname={{shellQuote .Description}}

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

start() {
	[ -x "$exec" ] || exit 5

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
		"$exec" {{.Args}} >/dev/null 2>&1 &
		pid=$!
		printf '%s\n' "$pid" > "$pidfile"
		sleep 1
		if is_expected_process; then
			touch "$lockfile"
			success
			echo
		else
			wait "$pid"
			retval=$?
			[ "$retval" -ne 0 ] || retval=1
			rm -f "$pidfile" "$lockfile"
			failure
			echo
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
	echo -n $"Stopping $servname: "
	if ! read_pid; then
		failure
		echo
		return 1
	fi
	if ! kill -0 "$pid" 2>/dev/null; then
		rm -f "$pidfile" "$lockfile"
		success
		echo
		return 0
	fi
	if ! is_expected_process; then
		failure
		echo
		return 1
	fi
	if ! kill -TERM "$pid" 2>/dev/null; then
		failure
		echo
		return 1
	fi

	elapsed=0
	while is_expected_process && [ "$elapsed" -lt "$stop_timeout" ]; do
		sleep 1
		elapsed=$((elapsed + 1))
	done
	if is_expected_process && ! kill -KILL "$pid" 2>/dev/null; then
		failure
		echo
		return 1
	fi
	force_elapsed=0
	while is_expected_process && [ "$force_elapsed" -lt 5 ]; do
		sleep 1
		force_elapsed=$((force_elapsed + 1))
	done
	if is_expected_process || kill -0 "$pid" 2>/dev/null; then
		failure
		echo
		return 1
	fi
	rm -f "$pidfile" "$lockfile"
	success
	echo
	return 0
}

restart() {
	stop && start
}

rh_status() {
    status -p $pidfile $proc
}

rh_status_q() {
    rh_status >/dev/null 2>&1
}

case "$1" in
    start)
        rh_status_q && exit 0
        $1
        ;;
    stop)
        rh_status_q || exit 0
        $1
        ;;
    restart)
        $1
        ;;
    status)
        rh_status
        ;;
    *)
        echo $"Usage: $0 {start|stop|status|restart}"
        exit 2
esac

exit $?
`
