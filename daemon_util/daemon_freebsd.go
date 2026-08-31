// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by
// license that can be found in the LICENSE file.

//go:build freebsd

package daemon_util

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"text/template"
)

// systemVRecord - standard record (struct) for linux systemV version of daemon package
type bsdRecord struct {
	serviceConfig
	name           string
	description    string
	executablePath string
	template       string
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

func (bsd *bsdRecord) getCmd(cmd string) string {
	if ok, err := bsd.isEnabled(); !ok || err != nil {
		fmt.Println("Service is not enabled, using one" + cmd + " instead")
		cmd = "one" + cmd
	}
	return cmd
}

// Get the daemon properly
func newDaemon(name, description string, _ Kind, _ []string, executablePath string) (Daemon, error) {
	return &bsdRecord{
		name:           name,
		description:    description,
		executablePath: executablePath,
		template:       defaultBSDConfig,
	}, nil
}

// ListServices returns user-facing names of services registered by this tool.
func ListServices() ([]string, error) {
	return listServiceFiles(serviceDirectory{path: "/usr/local/etc/rc.d"})
}

// ListServiceStatuses returns the status of services registered by this tool.
func ListServiceStatuses() ([]ServiceStatus, error) {
	return listServiceStatuses(serviceDirectory{
		path: "/usr/local/etc/rc.d",
		isRunning: func(name string) bool {
			return exec.Command("service", name, "onestatus").Run() == nil
		},
	})
}

// Check service is running
func (bsd *bsdRecord) checkRunning() (string, bool) {
	output, err := exec.Command("service", bsd.name, bsd.getCmd("status")).Output()
	if err == nil {
		reg := regexp.MustCompile("pid  ([0-9]+)")
		data := reg.FindStringSubmatch(string(output))
		if len(data) > 1 {
			return "Service (pid  " + data[1] + ") is running...", true
		}
		return "Service is running...", true
	}

	return "Service is stopped", false
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
		bsd.template,
		funcs,
		&struct {
			Name, RCName, RCVar, Description, Path, Args string
			StopTimeoutSeconds                           int64
		}{bsd.name, bsd.name, bsd.name + "_enable", bsd.description, execPatch, shellQuoteArgs(args), bsd.stopTimeoutSeconds()},
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

	if _, ok := bsd.checkRunning(); ok {
		return startAction + failed, ErrAlreadyRunning
	}

	if err := exec.Command("service", bsd.name, bsd.getCmd("start")).Run(); err != nil {
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

	if _, ok := bsd.checkRunning(); !ok {
		return stopAction + failed, ErrAlreadyStopped
	}

	if err := exec.Command("service", bsd.name, bsd.getCmd("stop")).Run(); err != nil {
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

	statusAction, _ := bsd.checkRunning()

	return statusAction, nil
}

// Run - Run service
func (bsd *bsdRecord) Run(e Executable) (string, error) {
	runAction := "Running " + bsd.description + ":"
	e.Run()
	return runAction + " completed.", nil
}

// GetTemplate - gets service config template
func (linux *bsdRecord) GetTemplate() string {
	return linux.template
}

// SetTemplate - sets service config template
func (linux *bsdRecord) SetTemplate(tplStr string) error {
	linux.template = tplStr
	return nil
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
pidfile="/var/run/$name.pid"
child_pidfile="/var/run/$name.child.pid"
stop_timeout={{.StopTimeoutSeconds}}

start_cmd="daemon_start"
stop_cmd="daemon_stop"
daemon_start()
{
	"$command" -R 30 -P "$pidfile" -p "$child_pidfile" -f "$app_command" {{.Args}}
}
daemon_stop()
{
	supervisor_pid=$rc_pid
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
