//go:build linux

package daemon_util

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

type openRCRecord struct {
	serviceConfig
	name           string
	description    string
	executablePath string
}

func openRCDetected(root string) bool {
	runInfo, err := os.Stat(filepath.Join(root, "run", "openrc"))
	if err != nil || !runInfo.IsDir() {
		return false
	}
	runnerInfo, err := os.Stat(filepath.Join(root, "sbin", "openrc-run"))
	return err == nil && runnerInfo.Mode().IsRegular() && runnerInfo.Mode().Perm()&0111 != 0
}

func (linux *openRCRecord) servicePath() string {
	return "/etc/init.d/" + linux.name
}

func (linux *openRCRecord) isInstalled() bool {
	_, err := os.Stat(linux.servicePath())
	return err == nil
}

func (linux *openRCRecord) runlevelCommand(action string) *exec.Cmd {
	return exec.Command("rc-update", action, linux.name, "default")
}

type openRCServiceState uint8

const (
	openRCServiceUnknown openRCServiceState = iota
	openRCServiceStopped
	openRCServiceStarted
	openRCServiceStopping
	openRCServiceStarting
	openRCServiceInactive
	openRCServiceCrashed
	openRCServiceUnsupervised
)

func (state openRCServiceState) running() bool {
	return state == openRCServiceStarted || state == openRCServiceStarting || state == openRCServiceStopping
}

func (state openRCServiceState) startable() bool {
	return state == openRCServiceStopped || state == openRCServiceInactive
}

func (state openRCServiceState) stoppable() bool {
	return state != openRCServiceUnknown && state != openRCServiceStopped
}

func (linux *openRCRecord) checkStatus() (string, openRCServiceState, error) {
	output, err := exec.Command("rc-service", linux.name, "status").CombinedOutput()
	state, recognized := openRCStatus(string(output), commandExitCode(err))
	if !recognized {
		return "", openRCServiceUnknown, statusCommandError("OpenRC", linux.name, output, err)
	}

	switch state {
	case openRCServiceStarted:
		return "Service is running...", state, nil
	case openRCServiceStopping:
		return "Service is stopping...", state, nil
	case openRCServiceStarting:
		return "Service is starting...", state, nil
	case openRCServiceInactive:
		return "Service is inactive", state, nil
	case openRCServiceCrashed:
		return "Service has crashed", state, nil
	case openRCServiceUnsupervised:
		return "Service is unsupervised", state, nil
	default:
		return "Service is stopped", state, nil
	}
}

func (linux *openRCRecord) checkRunning() (string, bool, error) {
	message, state, err := linux.checkStatus()
	return message, state.running(), err
}

func openRCStatus(status string, exitCode int) (openRCServiceState, bool) {
	status = strings.ToLower(strings.TrimSpace(status))
	tests := map[int]struct {
		text  string
		state openRCServiceState
	}{
		0:  {text: "status: started", state: openRCServiceStarted},
		3:  {text: "status: stopped", state: openRCServiceStopped},
		4:  {text: "status: stopping", state: openRCServiceStopping},
		8:  {text: "status: starting", state: openRCServiceStarting},
		16: {text: "status: inactive", state: openRCServiceInactive},
		32: {text: "status: crashed", state: openRCServiceCrashed},
		64: {text: "status: unsupervised", state: openRCServiceUnsupervised},
	}
	test, ok := tests[exitCode]
	if ok && strings.Contains(status, test.text) {
		return test.state, true
	}
	return openRCServiceUnknown, false
}

func (linux *openRCRecord) Install(args ...string) (string, error) {
	installAction := "Install " + linux.description + ":"

	if ok, err := checkPrivileges(); !ok {
		return installAction + failed, err
	}
	if linux.isInstalled() {
		return installAction + failed, ErrAlreadyInstalled
	}

	executablePath, err := resolveExecutablePath(linux.name, linux.executablePath)
	if err != nil {
		return installAction + failed, err
	}

	servicePath := linux.servicePath()
	if err := writeTemplateFile(
		servicePath,
		"openRCConfig",
		defaultOpenRCConfig,
		template.FuncMap{"shellQuote": shellQuote},
		&struct {
			Name, Description, Path, Args, WorkingDirectory string
			StopTimeoutSeconds                              int64
		}{linux.name, linux.description, executablePath, shellQuoteArgs(args), filepath.Dir(executablePath), linux.stopTimeoutSeconds()},
		0755,
	); err != nil {
		return installAction + failed, err
	}

	if err := linux.runlevelCommand("add").Run(); err != nil {
		if removeErr := os.Remove(servicePath); removeErr != nil {
			return installAction + failed, errors.Join(err, removeErr)
		}
		return installAction + failed, err
	}

	return installAction + success, nil
}

func (linux *openRCRecord) Remove() (string, error) {
	removeAction := "Removing " + linux.description + ":"

	if ok, err := checkPrivileges(); !ok {
		return removeAction + failed, err
	}
	if !linux.isInstalled() {
		return removeAction + failed, ErrNotInstalled
	}
	if err := linux.runlevelCommand("delete").Run(); err != nil {
		return removeAction + failed, err
	}
	if err := os.Remove(linux.servicePath()); err != nil {
		return removeAction + failed, err
	}

	return removeAction + success, nil
}

func (linux *openRCRecord) Start() (string, error) {
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
	if err := exec.Command("rc-service", linux.name, "start").Run(); err != nil {
		return startAction + failed, err
	}

	return startAction + success, nil
}

func (linux *openRCRecord) Stop() (string, error) {
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
	if err := exec.Command("rc-service", linux.name, "stop").Run(); err != nil {
		return stopAction + failed, err
	}

	return stopAction + success, nil
}

func (linux *openRCRecord) Status() (string, error) {
	if ok, err := checkPrivileges(); !ok {
		return "", err
	}
	if !linux.isInstalled() {
		return statNotInstalled, ErrNotInstalled
	}

	status, _, err := linux.checkStatus()
	return status, err
}

const defaultOpenRCConfig = `#!/sbin/openrc-run

name={{shellQuote .Name}}
description={{shellQuote .Description}}
command={{shellQuote .Path}}
command_args={{shellQuote .Args}}
directory={{shellQuote .WorkingDirectory}}
supervisor=supervise-daemon
stopgroup=true
respawn_delay=30
respawn_max=0
pidfile="/run/${RC_SVCNAME}.pid"
retry="TERM/{{.StopTimeoutSeconds}}/KILL/5"

depend() {
	need localmount
	after bootmisc
}

stop_pre() {
	daemon_stop_process_group=$(service_get_value child_pid)
}

stop_post() {
	[ -n "$daemon_stop_process_group" ] || return 0
	kill -KILL -- "-$daemon_stop_process_group" 2>/dev/null || true
}
`
