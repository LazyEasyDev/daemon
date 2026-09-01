// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by
// license that can be found in the LICENSE file.

//go:build linux

// Package daemon linux version
package daemon_util

import (
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var openwrtNameArr = []string{
	"wrt",
}

type linuxBackend struct {
	name             string
	warning          bool
	detected         func(string) bool
	serviceDirectory func(string) serviceDirectory
	newRecord        func(string, string, string) (Daemon, error)
}

var linuxBackends = []linuxBackend{
	{
		name: "systemd",
		detected: func(root string) bool {
			_, err := os.Stat(filepath.Join(root, "run/systemd/system"))
			return err == nil
		},
		serviceDirectory: func(root string) serviceDirectory {
			return serviceDirectory{
				path:   filepath.Join(root, "etc/systemd/system"),
				suffix: ".service",
				isRunning: func(name string) bool {
					_, running := (&systemDRecord{name: name}).checkRunning()
					return running
				},
			}
		},
		newRecord: func(name, description, executablePath string) (Daemon, error) {
			return &systemDRecord{name: name, description: description, executablePath: executablePath}, nil
		},
	},
	{
		name:     "openrc",
		detected: openRCDetected,
		serviceDirectory: func(root string) serviceDirectory {
			return serviceDirectory{
				path: filepath.Join(root, "etc/init.d"),
				isRunning: func(name string) bool {
					_, running := (&openRCRecord{name: name}).checkRunning()
					return running
				},
			}
		},
		newRecord: func(name, description, executablePath string) (Daemon, error) {
			return &openRCRecord{name: name, description: description, executablePath: executablePath}, nil
		},
	},
	{
		name: "upstart",
		detected: func(root string) bool {
			_, err := os.Stat(filepath.Join(root, "sbin/initctl"))
			return err == nil
		},
		serviceDirectory: func(root string) serviceDirectory {
			return serviceDirectory{
				path:   filepath.Join(root, "etc/init"),
				suffix: ".conf",
				isRunning: func(name string) bool {
					_, running := (&upstartRecord{name: name}).checkRunning()
					return running
				},
			}
		},
		newRecord: func(name, description, executablePath string) (Daemon, error) {
			return &upstartRecord{name: name, description: description, executablePath: executablePath}, nil
		},
	},
	{
		name: "openwrt",
		detected: func(root string) bool {
			return containsAny(linuxIdentity(root), openwrtNameArr)
		},
		serviceDirectory: func(root string) serviceDirectory {
			return serviceDirectory{
				path: filepath.Join(root, "etc/init.d"),
				isRunning: func(name string) bool {
					_, running := (&openWrtRecord{name: name}).checkRunning()
					return running
				},
			}
		},
		newRecord: func(name, description, executablePath string) (Daemon, error) {
			return &openWrtRecord{name: name, description: description, executablePath: executablePath}, nil
		},
	},
	{
		name:     "buildroot-style init",
		detected: buildrootStyleInitDetected,
		serviceDirectory: func(root string) serviceDirectory {
			return serviceDirectory{
				path:       filepath.Join(root, "etc/init.d"),
				filePrefix: "S90",
				isRunning: func(name string) bool {
					_, running := (&buildrootRecord{name: name}).checkRunning()
					return running
				},
			}
		},
		newRecord: func(name, description, executablePath string) (Daemon, error) {
			return &buildrootRecord{name: name, description: description, executablePath: executablePath}, nil
		},
	},
	{
		name:     "systemV",
		warning:  true,
		detected: systemVDetected,
		serviceDirectory: func(root string) serviceDirectory {
			return serviceDirectory{
				path: filepath.Join(root, "etc/init.d"),
				isRunning: func(name string) bool {
					_, running := (&systemVRecord{name: name}).checkRunning()
					return running
				},
			}
		},
		newRecord: func(name, description, executablePath string) (Daemon, error) {
			return &systemVRecord{name: name, description: description, executablePath: executablePath}, nil
		},
	},
}

// ListServiceStatuses returns the status of services registered by this tool.
func ListServiceStatuses() ([]ServiceStatus, error) {
	backend, err := detectLinuxBackend("/")
	if err != nil {
		return nil, err
	}
	return listServiceStatuses(backend.serviceDirectory("/"))
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return exitError.ExitCode()
	}
	return -1
}

// Get the daemon properly
func newDaemon(name, description string, _ Kind, executablePath string) (Daemon, error) {
	backend, err := detectLinuxBackend("/")
	if err != nil {
		return nil, err
	}
	record, err := backend.newRecord(name, description, executablePath)
	if err != nil {
		return nil, err
	}
	if backend.warning {
		log.Printf("[warning] using default %s type\n", backend.name)
	} else {
		log.Printf("[info] %s detected\n", backend.name)
	}
	return record, nil
}

func detectLinuxBackend(root string) (*linuxBackend, error) {
	for index := range linuxBackends {
		if linuxBackends[index].detected(root) {
			return &linuxBackends[index], nil
		}
	}
	return nil, ErrUnsupportedSystem
}

func systemVDetected(root string) bool {
	initDirectory, err := os.Stat(filepath.Join(root, "etc/init.d"))
	return err == nil && initDirectory.IsDir()
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

func linuxIdentity(root string) string {
	var identity strings.Builder
	for _, path := range []string{"/etc/os-release", "/usr/lib/os-release", "/etc/openwrt_release"} {
		content, err := os.ReadFile(filepath.Join(root, path))
		if err == nil {
			identity.Write(content)
			identity.WriteByte('\n')
		}
	}

	if filepath.Clean(root) == string(filepath.Separator) {
		output, err := exec.Command("uname", "-a").Output()
		if err == nil {
			identity.Write(output)
		}
	}
	return strings.ToLower(identity.String())
}
