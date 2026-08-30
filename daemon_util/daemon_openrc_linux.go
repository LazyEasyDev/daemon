//go:build linux

package daemon_util

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

type openRCRecord struct {
	name           string
	description    string
	executablePath string
	template       string
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

func (linux *openRCRecord) checkRunning() (string, bool) {
	if err := exec.Command("rc-service", linux.name, "status").Run(); err == nil {
		return "Service is running...", true
	}
	return "Service is stopped", false
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
	if err := validateExecutable(executablePath); err != nil {
		return installAction + failed, err
	}

	servicePath := linux.servicePath()
	if err := writeTemplateFile(
		servicePath,
		"openRCConfig",
		linux.template,
		template.FuncMap{"shellQuote": shellQuote},
		&struct {
			Name, Description, Path, Args string
		}{linux.name, linux.description, executablePath, shellQuoteArgs(args)},
		0755,
	); err != nil {
		return installAction + failed, err
	}

	if err := exec.Command("rc-update", "add", linux.name, "default").Run(); err != nil {
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
	if err := exec.Command("rc-update", "--all", "delete", linux.name).Run(); err != nil {
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
	if _, running := linux.checkRunning(); running {
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
	if _, running := linux.checkRunning(); !running {
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

	status, _ := linux.checkRunning()
	return status, nil
}

func (linux *openRCRecord) Run(executable Executable) (string, error) {
	runAction := "Running " + linux.description + ":"
	executable.Run()
	return runAction + " completed.", nil
}

func (linux *openRCRecord) GetTemplate() string {
	return linux.template
}

func (linux *openRCRecord) SetTemplate(template string) error {
	linux.template = template
	return nil
}

const defaultOpenRCConfig = `#!/sbin/openrc-run

name={{shellQuote .Name}}
description={{shellQuote .Description}}
command={{shellQuote .Path}}
command_args={{shellQuote .Args}}
command_background=yes
pidfile="/run/${RC_SVCNAME}.pid"
retry="TERM/12/KILL/5"

depend() {
	need localmount
	after bootmisc
}
`
