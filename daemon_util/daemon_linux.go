// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by
// license that can be found in the LICENSE file.

//go:build linux

// Package daemon linux version
package daemon_util

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var openwrtNameArr = []string{
	"wrt",
}

// ListServices returns user-facing names of services registered by this tool.
func ListServices() ([]string, error) {
	return listServiceFiles(
		serviceDirectory{path: "/etc/systemd/system", suffix: ".service"},
		serviceDirectory{path: "/etc/init", suffix: ".conf"},
		serviceDirectory{path: "/etc/init.d"},
		serviceDirectory{path: "/etc/init.d", filePrefix: "S90"},
	)
}

// ListServiceStatuses returns the status of services registered by this tool.
func ListServiceStatuses() ([]ServiceStatus, error) {
	return listServiceStatuses(
		serviceDirectory{
			path:   "/etc/systemd/system",
			suffix: ".service",
			isRunning: func(name string) bool {
				_, running := (&systemDRecord{name: name}).checkRunning()
				return running
			},
		},
		serviceDirectory{
			path:   "/etc/init",
			suffix: ".conf",
			isRunning: func(name string) bool {
				_, running := (&upstartRecord{name: name}).checkRunning()
				return running
			},
		},
		serviceDirectory{
			path: "/etc/init.d",
			isRunning: func(name string) bool {
				return exec.Command(filepath.Join("/etc/init.d", name), "status").Run() == nil
			},
		},
		serviceDirectory{
			path:       "/etc/init.d",
			filePrefix: "S90",
			isRunning: func(name string) bool {
				return exec.Command(filepath.Join("/etc/init.d", "S90"+name), "status").Run() == nil
			},
		},
	)
}

// Get the daemon properly
func newDaemon(name, description string, _ Kind, dependencies []string, executablePath string) (Daemon, error) {
	// newer subsystem must be checked first
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		if err := validateSystemdDependencies(dependencies); err != nil {
			return nil, err
		}
		log.Println("[info] systemd detected")
		return &systemDRecord{name: name, description: description, dependencies: dependencies, executablePath: executablePath, template: defaultSystemDConfig}, nil
	}
	if openRCDetected("/") {
		log.Println("[info] openrc detected")
		return &openRCRecord{name: name, description: description, executablePath: executablePath, template: defaultOpenRCConfig}, nil
	}
	if _, err := os.Stat("/sbin/initctl"); err == nil {
		log.Println("[info] upstart detected")
		return &upstartRecord{name: name, description: description, executablePath: executablePath, template: defaultUpstartConfig}, nil
	}

	identity := linuxIdentity()
	if containsAny(identity, openwrtNameArr) {
		log.Println("[info] openwrt detected")
		return &openWrtRecord{name: name, description: description, executablePath: executablePath, template: defaultOpenWrtConfig}, nil
	}

	if buildrootStyleInitDetected("/") {
		log.Println("[info] buildroot-style init detected")
		return &buildrootRecord{name: name, description: description, executablePath: executablePath, template: defaultBuildrootConfig}, nil
	}

	if info, err := os.Stat("/etc/rc.d/init.d/functions"); err == nil && !info.IsDir() {
		log.Println("[warning] using default systemV type")
		return &systemVRecord{name: name, description: description, executablePath: executablePath, template: defaultSystemVConfig}, nil
	}

	return nil, ErrUnsupportedSystem
}

func buildrootStyleInitDetected(root string) bool {
	rcSPath := filepath.Join(root, "etc/init.d/rcS")
	rcS, err := os.ReadFile(rcSPath)
	if err != nil || !strings.Contains(string(rcS), "/etc/init.d/S??*") {
		return false
	}

	for _, path := range []string{
		"sbin/start-stop-daemon",
		"usr/sbin/start-stop-daemon",
		"bin/start-stop-daemon",
		"usr/bin/start-stop-daemon",
	} {
		info, err := os.Stat(filepath.Join(root, path))
		if err == nil && !info.IsDir() && info.Mode().Perm()&0111 != 0 {
			return true
		}
	}

	return false
}

func containsAny(value string, identifiers []string) bool {
	for _, identifier := range identifiers {
		if strings.Contains(value, identifier) {
			return true
		}
	}
	return false
}

func linuxIdentity() string {
	var identity strings.Builder
	for _, path := range []string{"/etc/os-release", "/usr/lib/os-release", "/etc/openwrt_release"} {
		content, err := os.ReadFile(path)
		if err == nil {
			identity.Write(content)
			identity.WriteByte('\n')
		}
	}

	output, err := exec.Command("uname", "-a").Output()
	if err == nil {
		identity.Write(output)
	}
	return strings.ToLower(identity.String())
}
