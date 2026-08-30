// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by
// license that can be found in the LICENSE file.

package daemon_util

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"
)

const (
	success = " [OK]"
	failed  = " [FAILED]"
)

var (
	// ErrUnsupportedSystem appears if try to use service on system which is not supported by this release
	ErrUnsupportedSystem = errors.New("Unsupported system")

	// ErrRootPrivileges appears if run installation or deleting the service without root privileges
	ErrRootPrivileges = errors.New("You must have root user privileges. Possibly using 'sudo' command should help")

	// ErrAlreadyInstalled appears if service already installed on the system
	ErrAlreadyInstalled = errors.New("Service has already been installed")

	// ErrNotInstalled appears if try to delete service which was not been installed
	ErrNotInstalled = errors.New("Service is not installed")

	// ErrAlreadyRunning appears if try to start already running service
	ErrAlreadyRunning = errors.New("Service is already running")

	// ErrAlreadyStopped appears if try to stop already stopped service
	ErrAlreadyStopped = errors.New("Service has already been stopped")
)

// ExecPath tries to get executable path
func ExecPath() (string, error) {
	return os.Executable()
}

func resolveExecutablePath(name, configuredPath string) (string, error) {
	if configuredPath != "" {
		return configuredPath, nil
	}
	path, err := ExecPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), name), nil
}

type serviceDirectory struct {
	path       string
	filePrefix string
	suffix     string
	isRunning  func(string) bool
}

func listServiceFiles(directories ...serviceDirectory) ([]string, error) {
	names := make(map[string]struct{})
	for _, directory := range directories {
		entries, err := os.ReadDir(directory.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			filename := entry.Name()
			if !strings.HasPrefix(filename, directory.filePrefix) || !strings.HasSuffix(filename, directory.suffix) {
				continue
			}
			registrationName := strings.TrimPrefix(filename, directory.filePrefix)
			registrationName = strings.TrimSuffix(registrationName, directory.suffix)
			if logicalName, ok := logicalServiceName(registrationName); ok {
				names[logicalName] = struct{}{}
			}
		}
	}
	return sortedServiceNames(names), nil
}

func listServiceStatuses(directories ...serviceDirectory) ([]ServiceStatus, error) {
	statuses := make(map[string]string)
	for _, directory := range directories {
		entries, err := os.ReadDir(directory.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			filename := entry.Name()
			if !strings.HasPrefix(filename, directory.filePrefix) || !strings.HasSuffix(filename, directory.suffix) {
				continue
			}
			registrationName := strings.TrimPrefix(filename, directory.filePrefix)
			registrationName = strings.TrimSuffix(registrationName, directory.suffix)
			logicalName, ok := logicalServiceName(registrationName)
			if !ok {
				continue
			}

			status := ServiceStopped
			if directory.isRunning != nil && directory.isRunning(registrationName) {
				status = ServiceRunning
			}
			if current, exists := statuses[logicalName]; !exists || current != ServiceRunning {
				statuses[logicalName] = status
			}
		}
	}
	return sortedServiceStatuses(statuses), nil
}

func filterManagedServiceNames(registrationNames []string) []string {
	names := make(map[string]struct{})
	for _, registrationName := range registrationNames {
		if logicalName, ok := logicalServiceName(registrationName); ok {
			names[logicalName] = struct{}{}
		}
	}
	return sortedServiceNames(names)
}

func sortedServiceNames(names map[string]struct{}) []string {
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func sortedServiceStatuses(statuses map[string]string) []ServiceStatus {
	names := make([]string, 0, len(statuses))
	for name := range statuses {
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([]ServiceStatus, 0, len(statuses))
	for _, name := range names {
		result = append(result, ServiceStatus{Name: name, Status: statuses[name]})
	}
	return result
}

func quoteArgs(args []string, quote func(string) string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = quote(arg)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func shellQuoteArgs(args []string) string {
	return quoteArgs(args, shellQuote)
}

func freeBSDRCName(name string) string {
	valid := name != ""
	for index, character := range name {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character == '_' ||
			index > 0 && character >= '0' && character <= '9' {
			continue
		}
		valid = false
		break
	}
	if valid {
		return name
	}

	var encoded strings.Builder
	encoded.WriteString("daemon")
	for _, character := range name {
		encoded.WriteByte('_')
		encoded.WriteString(strconv.FormatInt(int64(character), 16))
	}
	return encoded.String()
}

func freeBSDRCVar(name string) string {
	return freeBSDRCName(name) + "_enable"
}

func systemdQuote(value string) string {
	quoted := strconv.Quote(value)
	quoted = strings.ReplaceAll(quoted, "$", "$$")
	return strings.ReplaceAll(quoted, "%", "%%")
}

func systemdQuoteArgs(args []string) string {
	return quoteArgs(args, systemdQuote)
}

func systemdConfigQuote(value string) string {
	return strings.ReplaceAll(strconv.Quote(value), "%", "%%")
}

func systemdConfigQuoteArgs(args []string) string {
	return quoteArgs(args, systemdConfigQuote)
}

func validateSystemdDependencies(dependencies []string) error {
	for _, dependency := range dependencies {
		if dependency == "" {
			return fmt.Errorf("%w: name must not be empty", ErrInvalidDependency)
		}
		for _, character := range dependency {
			if character >= 'a' && character <= 'z' ||
				character >= 'A' && character <= 'Z' ||
				character >= '0' && character <= '9' ||
				strings.ContainsRune(":_.@-", character) {
				continue
			}
			return fmt.Errorf("%w %q: unsupported character %q", ErrInvalidDependency, dependency, character)
		}
		separator := strings.LastIndexByte(dependency, '.')
		if separator <= 0 || separator == len(dependency)-1 {
			return fmt.Errorf("%w %q: unit type suffix is required", ErrInvalidDependency, dependency)
		}
	}
	return nil
}

func writeTemplateFile(path, name, source string, funcs template.FuncMap, data any, mode fs.FileMode) error {
	templ, err := template.New(name).Funcs(funcs).Parse(source)
	if err != nil {
		return err
	}

	var content bytes.Buffer
	if err := templ.Execute(&content, data); err != nil {
		return err
	}

	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	defer temporary.Close()

	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if _, err := temporary.Write(content.Bytes()); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}

	return os.Rename(temporaryPath, path)
}
