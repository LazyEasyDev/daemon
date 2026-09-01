// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by
// license that can be found in the LICENSE file.

//go:build linux

package daemon_util

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"text/template"
)

// upstartRecord - standard record (struct) for linux upstart version of daemon package
type upstartRecord struct {
	serviceConfig
	name           string
	description    string
	executablePath string
	template       string
}

// Standard service path for systemV daemons
func (linux *upstartRecord) servicePath() string {
	return "/etc/init/" + linux.name + ".conf"
}

// Is a service installed
func (linux *upstartRecord) isInstalled() bool {

	if _, err := os.Stat(linux.servicePath()); err == nil {
		return true
	}

	return false
}

// Check service is running
func (linux *upstartRecord) checkRunning() (string, bool) {
	output, err := exec.Command("status", linux.name).Output()
	if err == nil && upstartStatusActive(linux.name, string(output)) {
		reg := regexp.MustCompile("process ([0-9]+)")
		data := reg.FindStringSubmatch(string(output))
		if len(data) > 1 {
			return "Service (pid  " + data[1] + ") is running...", true
		}
		return "Service is running...", true
	}

	return "Service is stopped", false
}

func upstartStatusActive(name, status string) bool {
	matched, err := regexp.MatchString(`^`+regexp.QuoteMeta(name)+` start/`, status)
	return err == nil && matched
}

// Install the service
func (linux *upstartRecord) Install(args ...string) (string, error) {
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
		"shellQuote":   shellQuote,
		"upstartQuote": upstartQuote,
	}
	if err := writeTemplateFile(
		srvPath,
		"upstartConfig",
		linux.template,
		funcs,
		&struct {
			Name, Description, Path, Args, WorkingDirectory string
			StopTimeoutSeconds                              int64
		}{linux.name, linux.description, execPatch, shellQuoteArgs(args), filepath.Dir(execPatch), linux.stopTimeoutSeconds()},
		0644,
	); err != nil {
		return installAction + failed, err
	}

	return installAction + success, nil
}

// Remove the service
func (linux *upstartRecord) Remove() (string, error) {
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
func (linux *upstartRecord) Start() (string, error) {
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

	if err := exec.Command("start", linux.name).Run(); err != nil {
		return startAction + failed, err
	}

	return startAction + success, nil
}

// Stop the service
func (linux *upstartRecord) Stop() (string, error) {
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

	if err := exec.Command("stop", linux.name).Run(); err != nil {
		return stopAction + failed, err
	}

	return stopAction + success, nil
}

// Status - Get service status
func (linux *upstartRecord) Status() (string, error) {

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
func (linux *upstartRecord) Run(e Executable) (string, error) {
	runAction := "Running " + linux.description + ":"
	e.Run()
	return runAction + " completed.", nil
}

const defaultUpstartConfig = `# {{.Name}} {{.Description}}

description     {{shellQuote .Description}}

start on runlevel [2345]
stop on runlevel [016]

respawn
respawn limit 0 5
kill timeout {{.StopTimeoutSeconds}}
chdir {{upstartQuote .WorkingDirectory}}

exec {{shellQuote .Path}} {{.Args}}
`
