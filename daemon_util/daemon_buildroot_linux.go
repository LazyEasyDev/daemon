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

	if err := disableInstalledWatcher(linux.servicePath()); err != nil {
		return removeAction + failed, err
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
INIT_SCRIPT=${INIT_SCRIPT:-/etc/init.d/S90$NAME}
PIDFILE=${PIDFILE:-/var/run/$NAME.pid}
RESTART_DELAY=${RESTART_DELAY:-30}
STOP_TIMEOUT={{.StopTimeoutSeconds}}

[ -r "/etc/default/$NAME" ] && . "/etc/default/$NAME" "$1"

WATCHER_PIDFILE=${WATCHER_PIDFILE:-${PIDFILE%.pid}.watchdog.pid}

case "$RESTART_DELAY" in
	''|0|*[!0-9]*) RESTART_DELAY=30 ;;
esac

read_pid() {
	[ -r "$PIDFILE" ] || return 1
	pid=$(cat "$PIDFILE")
	case "$pid" in
		''|*[!0-9]*) return 1 ;;
	esac
	[ "$pid" -gt 1 ]
}

read_watcher_pid() {
	[ -r "$WATCHER_PIDFILE" ] || return 1
	watcher_pid=$(cat "$WATCHER_PIDFILE")
	case "$watcher_pid" in
		''|*[!0-9]*) return 1 ;;
	esac
	[ "$watcher_pid" -gt 1 ]
}

is_watcher_process() {
	[ -r "/proc/$watcher_pid/cmdline" ] || return 1
	watcher_identity=$(tr '\000' '\n' < "/proc/$watcher_pid/cmdline" | sed -n '2,3p')
	[ "$watcher_identity" = "$INIT_SCRIPT
watch" ]
}

watcher_owns_pidfile() {
	read_watcher_pid && [ "$watcher_pid" -eq "$$" ]
}

acquire_watcher_pidfile() {
	(umask 022; set -C; printf '%s\n' "$$" > "$WATCHER_PIDFILE") 2>/dev/null
}

cleanup_watcher() {
	if watcher_owns_pidfile; then
		rm -f "$WATCHER_PIDFILE"
	fi
}

stop_watcher_loop() {
	if [ -n "$watcher_sleep_pid" ]; then
		kill "$watcher_sleep_pid" 2>/dev/null
		wait "$watcher_sleep_pid" 2>/dev/null
	fi
	exit 0
}

watcher_sleep() {
	sleep "$1" &
	watcher_sleep_pid=$!
	wait "$watcher_sleep_pid" 2>/dev/null
	watcher_sleep_pid=
}

watch() {
	acquire_watcher_pidfile || return 0
	watcher_sleep_pid=
	trap 'stop_watcher_loop' HUP INT TERM
	trap 'cleanup_watcher' 0

	while watcher_owns_pidfile; do
		if is_running; then
			watcher_sleep 1
			continue
		fi
		watcher_sleep "$RESTART_DELAY"
		watcher_owns_pidfile || break
		if ! is_running; then
			"$INIT_SCRIPT" start watched >/dev/null 2>&1
		fi
	done
}

start_watcher() {
	if [ -e "$WATCHER_PIDFILE" ]; then
		if read_watcher_pid && is_watcher_process; then
			return 0
		fi
		rm -f "$WATCHER_PIDFILE" || return 1
	fi
	"$INIT_SCRIPT" watch >/dev/null 2>&1 &

	elapsed=0
	while [ "$elapsed" -lt 5 ]; do
		if read_watcher_pid && is_watcher_process; then
			return 0
		fi
		sleep 1
		elapsed=$((elapsed + 1))
	done
	return 1
}

disable_watcher() {
	[ -e "$WATCHER_PIDFILE" ] || return 0
	if ! read_watcher_pid || ! is_watcher_process; then
		rm -f "$WATCHER_PIDFILE"
		return $?
	fi
	rm -f "$WATCHER_PIDFILE" || return 1
	if ! kill -TERM "$watcher_pid" 2>/dev/null; then
		is_watcher_process && return 1
		return 0
	fi

	elapsed=0
	while is_watcher_process && [ "$elapsed" -lt 5 ]; do
		sleep 1
		elapsed=$((elapsed + 1))
	done
	if is_watcher_process && ! kill -KILL "$watcher_pid" 2>/dev/null; then
		return 1
	fi
	return 0
}

is_running() {
	read_pid && start-stop-daemon -K -t -q -p "$PIDFILE" -x "$DAEMON"
}

is_pid_running() {
	read_pid && start-stop-daemon -K -t -q -p "$PIDFILE"
}

start_from_watch() {
	echo -n "Starting $NAME: "
	if ! cd "$WORKING_DIRECTORY"; then
		echo "FAIL"
		return 1
	fi
	if [ -f "$PIDFILE" ] && ! is_running; then
		if read_pid && start-stop-daemon -K -t -q -p "$PIDFILE"; then
			echo "FAIL"
			return 1
		fi
		rm -f "$PIDFILE" || return 1
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

start() {
	if ! is_running; then
		start_from_watch || return $?
	fi
	if ! start_watcher; then
		echo "Warning: $NAME watcher could not start" >&2
	fi
	return 0
}

stop() {
	echo -n "Stopping $NAME: "
	if ! disable_watcher; then
		echo "FAIL"
		return 1
	fi
	if ! read_pid; then
		rm -f "$PIDFILE"
		echo "OK"
		return 0
	fi
	if ! is_running; then
		if is_pid_running; then
			echo "FAIL"
			return 1
		fi
		rm -f "$PIDFILE"
		echo "OK"
		return 0
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
	if is_pid_running; then
		echo "$NAME is running (unverified pid $pid)"
		return 0
	fi
	rm -f "$PIDFILE"
	if read_watcher_pid && is_watcher_process; then
		echo "$NAME is running (watcher pid $watcher_pid)"
		return 0
	fi
	rm -f "$WATCHER_PIDFILE"
	echo "$NAME is stopped"
	return 3
}

case "$1" in
	start)
		if [ "$2" = "watched" ]; then
			is_running || start_from_watch
		else
			start
		fi
		;;
	stop)
		stop
		;;
	restart)
		stop &&
			sleep 12 &&
			start
		;;
	status)
		do_status
		;;
	watch)
		watch
		;;
	unwatch)
		disable_watcher
		;;
	*)
		echo "Usage: $0 {start|stop|restart|status}"
		exit 1
esac
`
