// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by
// license that can be found in the LICENSE file.

//go:build linux

package daemon_util

import (
	"os"
	"os/exec"
	"text/template"
)

// systemDRecord - standard record (struct) for linux systemD version of daemon package
type systemDRecord struct {
	name           string
	description    string
	dependencies   []string
	executablePath string
	template       string
}

// Standard service path for systemD daemons
func (linux *systemDRecord) servicePath() string {
	return "/etc/systemd/system/" + linux.name + ".service"
}

// Is a service installed
func (linux *systemDRecord) isInstalled() bool {

	if _, err := os.Stat(linux.servicePath()); err == nil {
		return true
	}

	return false
}

// Check service is running
func (linux *systemDRecord) checkRunning() (string, bool) {
	if err := exec.Command("systemctl", "is-active", "--quiet", linux.name+".service").Run(); err == nil {
		return "Service is running...", true
	}

	return "Service is stopped", false
}

// Install the service
func (linux *systemDRecord) Install(args ...string) (string, error) {
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
		"systemdQuote":       systemdQuote,
		"systemdConfigQuote": systemdConfigQuote,
	}
	if err := writeTemplateFile(
		srvPath,
		"systemDConfig",
		linux.template,
		funcs,
		&struct {
			Name, Description, Dependencies, Path, Args string
		}{
			linux.name,
			linux.description,
			systemdConfigQuoteArgs(linux.dependencies),
			execPatch,
			systemdQuoteArgs(args),
		},
		0644,
	); err != nil {
		return installAction + failed, err
	}

	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		_ = os.Remove(srvPath)
		return installAction + failed, err
	}

	if err := exec.Command("systemctl", "enable", linux.name+".service").Run(); err != nil {
		_ = exec.Command("systemctl", "disable", linux.name+".service").Run()
		_ = os.Remove(srvPath)
		_ = exec.Command("systemctl", "daemon-reload").Run()
		return installAction + failed, err
	}

	return installAction + success, nil
}

// Remove the service
func (linux *systemDRecord) Remove() (string, error) {
	removeAction := "Removing " + linux.description + ":"

	if ok, err := checkPrivileges(); !ok {
		return removeAction + failed, err
	}

	if !linux.isInstalled() {
		return removeAction + failed, ErrNotInstalled
	}

	if err := exec.Command("systemctl", "disable", linux.name+".service").Run(); err != nil {
		return removeAction + failed, err
	}

	if err := os.Remove(linux.servicePath()); err != nil {
		return removeAction + failed, err
	}
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return removeAction + failed, err
	}

	return removeAction + success, nil
}

// Start the service
func (linux *systemDRecord) Start() (string, error) {
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

	if err := exec.Command("systemctl", "start", linux.name+".service").Run(); err != nil {
		return startAction + failed, err
	}

	return startAction + success, nil
}

// Stop the service
func (linux *systemDRecord) Stop() (string, error) {
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

	if err := exec.Command("systemctl", "stop", linux.name+".service").Run(); err != nil {
		return stopAction + failed, err
	}

	return stopAction + success, nil
}

// Status - Get service status
func (linux *systemDRecord) Status() (string, error) {

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
func (linux *systemDRecord) Run(e Executable) (string, error) {
	runAction := "Running " + linux.description + ":"
	e.Run()
	return runAction + " completed.", nil
}

// GetTemplate - gets service config template
func (linux *systemDRecord) GetTemplate() string {
	return linux.template
}

// SetTemplate - sets service config template
func (linux *systemDRecord) SetTemplate(tplStr string) error {
	linux.template = tplStr
	return nil
}

const defaultSystemDConfig = `[Unit]
Description={{systemdConfigQuote .Description}}
Requires={{.Dependencies}}
After={{.Dependencies}}

[Service]
Type=exec
ExecStart={{systemdQuote .Path}} {{.Args}}
Restart=on-failure
RestartSec=20s

[Install]
WantedBy=multi-user.target
`
