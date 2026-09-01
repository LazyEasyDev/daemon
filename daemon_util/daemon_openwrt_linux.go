// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by
// license that can be found in the LICENSE file.

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
	template       string
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

// Check service is running
func (linux *openWrtRecord) checkRunning() (string, bool) {
	srvPath := linux.servicePath()
	output, err := exec.Command(srvPath, "status").Output()
	if openWrtStatusActive(string(output), commandExitCode(err)) {
		return "Service is running...", true
	}

	return "Service is stopped", false
}

func openWrtStatusActive(status string, exitCode int) bool {
	status = strings.TrimSpace(status)
	return exitCode == 0 && (status == "running" || strings.HasPrefix(status, "running ("))
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
		linux.template,
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
func (linux *openWrtRecord) Stop() (string, error) {
	stopAction := "Stopping " + linux.description + ":"

	if ok, err := checkPrivileges(); !ok {
		return stopAction + failed, err
	}

	if !linux.isInstalled() {
		return stopAction + failed, ErrNotInstalled
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

	statusAction, _ := linux.checkRunning()

	return statusAction, nil
}

// Run - Run service
func (linux *openWrtRecord) Run(e Executable) (string, error) {
	return runExecutable(linux.description, e)
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

start_service() {
	echo "start ${DAEMON} service!"

	# ubus call service list -check instance
	procd_open_instance

	# threshold:0; timeout:30; retry:0 (unlimited)
	procd_set_param respawn 0 30 0
	procd_set_param term_timeout {{.StopTimeoutSeconds}}
	
	# run
	procd_set_param command /bin/sh -c 'cd "$1" && shift && exec "$@"' sh "$WORKING_DIRECTORY" "$PROG" {{.Args}}

	procd_close_instance
}

restart() {
	stop && start
}

`
