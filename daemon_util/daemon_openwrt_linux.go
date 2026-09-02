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

// openWrtRecord - standard record (struct) for linux openWrtRecord version of daemon package
type openWrtRecord struct {
	serviceConfig
	name           string
	description    string
	executablePath string
}

// Standard service path for systemV daemons
func (linux *openWrtRecord) servicePath() string {
	return "/etc/init.d/" + linux.name
}

// Is a service installed
func (linux *openWrtRecord) isInstalled() bool {

	if _, err := os.Stat(linux.servicePath()); err == nil {
		return true
	}

	return false
}

type openWrtServiceState uint8

const (
	openWrtServiceUnknown openWrtServiceState = iota
	openWrtServiceInactive
	openWrtServiceNotRunning
	openWrtServiceRunning
)

func (state openWrtServiceState) running() bool {
	return state == openWrtServiceRunning
}

func (state openWrtServiceState) startable() bool {
	return state == openWrtServiceInactive || state == openWrtServiceNotRunning
}

func (state openWrtServiceState) stoppable() bool {
	return state == openWrtServiceRunning || state == openWrtServiceNotRunning
}

func (linux *openWrtRecord) checkStatus() (string, openWrtServiceState, error) {
	srvPath := linux.servicePath()
	output, err := exec.Command(srvPath, "status").CombinedOutput()
	state, recognized := openWrtStatus(string(output), commandExitCode(err))
	if !recognized {
		return "", openWrtServiceUnknown, statusCommandError("OpenWrt", linux.name, output, err)
	}
	if state == openWrtServiceRunning {
		return "Service is running...", state, nil
	}
	return "Service is stopped", state, nil
}

// Check service is running
func (linux *openWrtRecord) checkRunning() (string, bool, error) {
	message, state, err := linux.checkStatus()
	return message, state.running(), err
}

func openWrtStatus(status string, exitCode int) (openWrtServiceState, bool) {
	status = strings.TrimSpace(status)
	if exitCode == 0 && (status == "running" || strings.HasPrefix(status, "running (") || status == "active with no instances") {
		return openWrtServiceRunning, true
	}
	if exitCode == 3 && status == "inactive" {
		return openWrtServiceInactive, true
	}
	if exitCode == 5 && status == "not running" {
		return openWrtServiceNotRunning, true
	}
	return openWrtServiceUnknown, false
}

// Install the service
func (linux *openWrtRecord) Install(args ...string) (string, error) {
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
		"openWrtConfig",
		defaultOpenWrtConfig,
		funcs,
		&struct {
			Name, Description, Path, Args, WorkingDirectory string
			StopTimeoutSeconds                              int64
		}{linux.name, linux.description, execPatch, shellQuoteArgs(args), filepath.Dir(execPatch), linux.stopTimeoutSeconds()},
		0755,
	); err != nil {
		return installAction + failed, err
	}

	if err := exec.Command(srvPath, "enable").Run(); err != nil {
		_ = os.Remove(srvPath)
		return installAction + failed, err
	}

	return installAction + success, nil
}

// Remove the service
func (linux *openWrtRecord) Remove() (string, error) {
	removeAction := "Removing " + linux.description + ":"

	if ok, err := checkPrivileges(); !ok {
		return removeAction + failed, err
	}

	if !linux.isInstalled() {
		return removeAction + failed, ErrNotInstalled
	}

	srvPath := linux.servicePath()
	if err := exec.Command(srvPath, "disable").Run(); err != nil {
		return removeAction + failed, err
	}

	if err := os.Remove(linux.servicePath()); err != nil {
		return removeAction + failed, err
	}

	return removeAction + success, nil
}

// Start the service
func (linux *openWrtRecord) Start() (string, error) {
	startAction := "Starting " + linux.description + ":"

	if ok, err := checkPrivileges(); !ok {
		return startAction + failed, err
	}

	if !linux.isInstalled() {
		return startAction + failed, ErrNotInstalled
	}

	_, state, err := linux.checkStatus()
	if err != nil {
		return startAction + failed, err
	}
	if !state.startable() {
		return startAction + failed, ErrAlreadyRunning
	}

	srvPath := linux.servicePath()
	if err := exec.Command(srvPath, "start").Run(); err != nil {
		return startAction + failed, err
	}

	return startAction + success, nil
}

// Stop the service
func (linux *openWrtRecord) Stop() (string, error) {
	stopAction := "Stopping " + linux.description + ":"

	if ok, err := checkPrivileges(); !ok {
		return stopAction + failed, err
	}

	if !linux.isInstalled() {
		return stopAction + failed, ErrNotInstalled
	}
	_, state, err := linux.checkStatus()
	if err != nil {
		return stopAction + failed, err
	}
	if !state.stoppable() {
		return stopAction + failed, ErrAlreadyStopped
	}

	srvPath := linux.servicePath()
	if err := exec.Command(srvPath, "stop").Run(); err != nil {
		return stopAction + failed, err
	}

	return stopAction + success, nil
}

// Status - Get service status
func (linux *openWrtRecord) Status() (string, error) {

	if ok, err := checkPrivileges(); !ok {
		return "", err
	}

	if !linux.isInstalled() {
		return statNotInstalled, ErrNotInstalled
	}

	statusAction, _, err := linux.checkStatus()
	return statusAction, err
}

const defaultOpenWrtConfig = `#!/bin/sh /etc/rc.common
#
#       /etc/init.d/{{.Name}}
#
#       Starts {{.Name}} as a daemon
#
# Copyright (C) 2008 OpenWrt.org
# description: Starts and stops a single {{.Name}} instance on this system

START=98
STOP=01

USE_PROCD=1

DAEMON={{shellQuote .Name}}
PROG={{shellQuote .Path}}
WORKING_DIRECTORY={{shellQuote .WorkingDirectory}}
STOP_TIMEOUT={{.StopTimeoutSeconds}}

start_service() {
	echo "start ${DAEMON} service!"

	# ubus call service list -check instance
	procd_open_instance

	# threshold:0; timeout:30; retry:0 (unlimited)
	procd_set_param respawn 0 30 0
	procd_set_param term_timeout "$STOP_TIMEOUT"
	
	# run
	procd_set_param command /bin/sh -c 'cd "$1" && shift && exec "$@"' sh "$WORKING_DIRECTORY" "$PROG" {{.Args}}

	procd_close_instance
}

service_stopped() {
	elapsed=0
	while procd_running "$DAEMON"; do
		if [ "$elapsed" -ge $((STOP_TIMEOUT + 5)) ]; then
			echo "$DAEMON failed to stop" >&2
			return 1
		fi
		sleep 1
		elapsed=$((elapsed + 1))
	done
}

restart() {
	stop && start
}

`
