// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by
// license that can be found in the LICENSE file.

//go:build darwin

// Package daemon darwin (mac os x) version
package daemon_util

import (
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"text/template"
)

// darwinRecord - standard record (struct) for darwin version of daemon package
type darwinRecord struct {
	name           string
	description    string
	kind           Kind
	executablePath string
	path           string
	template       string
}

func newDaemon(name, description string, kind Kind, _ []string, executablePath string) (Daemon, error) {
	var path string
	switch kind {
	case UserAgent:
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, "Library", "LaunchAgents", name+".plist")
	case GlobalAgent:
		path = filepath.Join("/Library/LaunchAgents", name+".plist")
	case GlobalDaemon:
		path = filepath.Join("/Library/LaunchDaemons", name+".plist")
	}

	return &darwinRecord{
		name:           name,
		description:    description,
		kind:           kind,
		executablePath: executablePath,
		path:           path,
		template:       defaultPropertyList,
	}, nil
}

// ListServices returns user-facing names of services registered by this tool.
func ListServices() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return listServiceFiles(
		serviceDirectory{path: filepath.Join(home, "Library", "LaunchAgents"), suffix: ".plist"},
		serviceDirectory{path: "/Library/LaunchAgents", suffix: ".plist"},
		serviceDirectory{path: "/Library/LaunchDaemons", suffix: ".plist"},
	)
}

// ListServiceStatuses returns the status of services registered by this tool.
func ListServiceStatuses() ([]ServiceStatus, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	isRunning := func(name string) bool {
		_, running := (&darwinRecord{name: name}).checkRunning()
		return running
	}
	return listServiceStatuses(
		serviceDirectory{path: filepath.Join(home, "Library", "LaunchAgents"), suffix: ".plist", isRunning: isRunning},
		serviceDirectory{path: "/Library/LaunchAgents", suffix: ".plist", isRunning: isRunning},
		serviceDirectory{path: "/Library/LaunchDaemons", suffix: ".plist", isRunning: isRunning},
	)
}

// Standard service path for system daemons
func (darwin *darwinRecord) servicePath() string {
	return darwin.path
}

// Is a service installed
func (darwin *darwinRecord) isInstalled() bool {

	if _, err := os.Stat(darwin.servicePath()); err == nil {
		return true
	}

	return false
}

// Check service is running
func (darwin *darwinRecord) checkRunning() (string, bool) {
	output, err := exec.Command("launchctl", "list", darwin.name).Output()
	if err == nil {
		if matched, err := regexp.MatchString(darwin.name, string(output)); err == nil && matched {
			reg := regexp.MustCompile("PID\" = ([0-9]+);")
			data := reg.FindStringSubmatch(string(output))
			if len(data) > 1 {
				return "Service (pid  " + data[1] + ") is running...", true
			}
		}
	}

	return "Service is stopped", false
}

// Install the service
func (darwin *darwinRecord) Install(args ...string) (string, error) {
	installAction := "Install " + darwin.description + ":"

	ok, err := checkPrivileges()
	if !ok && darwin.kind != UserAgent {
		return installAction + failed, err
	}

	srvPath := darwin.servicePath()

	if darwin.isInstalled() {
		return installAction + failed, ErrAlreadyInstalled
	}
	if err := os.MkdirAll(filepath.Dir(srvPath), 0755); err != nil {
		return installAction + failed, err
	}

	execPatch, err := resolveExecutablePath(darwin.name, darwin.executablePath)
	if err != nil {
		return installAction + failed, err
	}
	_, err = os.Stat(execPatch)
	if err != nil {
		return installAction + failed, err
	}

	funcs := template.FuncMap{
		"xml": html.EscapeString,
	}
	if err := writeTemplateFile(
		srvPath,
		"propertyList",
		darwin.template,
		funcs,
		&struct {
			Name, Path, WorkingDirectory string
			Args                         []string
		}{darwin.name, execPatch, filepath.Dir(execPatch), args},
		0644,
	); err != nil {
		return installAction + failed, err
	}

	return installAction + success, nil
}

// Remove the service
func (darwin *darwinRecord) Remove() (string, error) {
	removeAction := "Removing " + darwin.description + ":"

	ok, err := checkPrivileges()
	if !ok && darwin.kind != UserAgent {
		return removeAction + failed, err
	}

	if !darwin.isInstalled() {
		return removeAction + failed, ErrNotInstalled
	}

	if err := os.Remove(darwin.servicePath()); err != nil {
		return removeAction + failed, err
	}

	return removeAction + success, nil
}

// Start the service
func (darwin *darwinRecord) Start() (string, error) {
	startAction := "Starting " + darwin.description + ":"

	ok, err := checkPrivileges()
	if !ok && darwin.kind != UserAgent {
		return startAction + failed, err
	}

	if !darwin.isInstalled() {
		return startAction + failed, ErrNotInstalled
	}

	if _, ok := darwin.checkRunning(); ok {
		return startAction + failed, ErrAlreadyRunning
	}

	if err := exec.Command("launchctl", "load", darwin.servicePath()).Run(); err != nil {
		return startAction + failed, err
	}

	return startAction + success, nil
}

// Stop the service
func (darwin *darwinRecord) Stop() (string, error) {
	stopAction := "Stopping " + darwin.description + ":"

	ok, err := checkPrivileges()
	if !ok && darwin.kind != UserAgent {
		return stopAction + failed, err
	}

	if !darwin.isInstalled() {
		return stopAction + failed, ErrNotInstalled
	}

	if _, ok := darwin.checkRunning(); !ok {
		return stopAction + failed, ErrAlreadyStopped
	}

	if err := exec.Command("launchctl", "unload", darwin.servicePath()).Run(); err != nil {
		return stopAction + failed, err
	}

	return stopAction + success, nil
}

// Status - Get service status
func (darwin *darwinRecord) Status() (string, error) {

	ok, err := checkPrivileges()
	if !ok && darwin.kind != UserAgent {
		return "", err
	}

	if !darwin.isInstalled() {
		return statNotInstalled, ErrNotInstalled
	}

	statusAction, _ := darwin.checkRunning()

	return statusAction, nil
}

// Run - Run service
func (darwin *darwinRecord) Run(e Executable) (string, error) {
	runAction := "Running " + darwin.description + ":"
	e.Run()
	return runAction + " completed.", nil
}

// GetTemplate - gets service config template
func (linux *darwinRecord) GetTemplate() string {
	return linux.template
}

// SetTemplate - sets service config template
func (linux *darwinRecord) SetTemplate(tplStr string) error {
	linux.template = tplStr
	return nil
}

const defaultPropertyList = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>KeepAlive</key>
	<true/>
	<key>Label</key>
	<string>{{xml .Name}}</string>
	<key>ProgramArguments</key>
	<array>
	    <string>{{xml .Path}}</string>
		{{range .Args}}<string>{{xml .}}</string>
		{{end}}
	</array>
	<key>RunAtLoad</key>
	<true/>
    <key>WorkingDirectory</key>
	<string>{{xml .WorkingDirectory}}</string>
</dict>
</plist>
`
