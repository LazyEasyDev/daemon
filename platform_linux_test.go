//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSELinuxInstallWarning(t *testing.T) {
	enforcePath := filepath.Join(t.TempDir(), "enforce")
	if err := os.WriteFile(enforcePath, []byte("1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	executablePath := "/home/user/app"
	warning := selinuxInstallWarning(executablePath, enforcePath, func(string) (string, error) {
		return "unconfined_u:object_r:user_home_t:s0", nil
	})
	if !strings.Contains(warning, executablePath) || !strings.Contains(warning, "user_home_t") || !strings.Contains(warning, "/opt/<application>") {
		t.Fatalf("warning = %q, want executable path, context, and remediation", warning)
	}
}

func TestSELinuxInstallWarningCoversRiskyTypesAndPaths(t *testing.T) {
	enforcePath := filepath.Join(t.TempDir(), "enforce")
	if err := os.WriteFile(enforcePath, []byte("1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		path       string
		context    string
		contextErr error
	}{
		{name: "user temporary type", path: "/srv/app", context: "unconfined_u:object_r:user_tmp_t:s0"},
		{name: "temporary type", path: "/srv/app", context: "system_u:object_r:tmp_t:s0"},
		{name: "administrator home type", path: "/srv/app", context: "system_u:object_r:admin_home_t:s0"},
		{name: "unlabeled type", path: "/srv/app", context: "system_u:object_r:unlabeled_t:s0"},
		{name: "default type", path: "/srv/app", context: "system_u:object_r:default_t:s0"},
		{name: "generic variable data type", path: "/srv/app", context: "system_u:object_r:var_t:s0"},
		{name: "network filesystem type", path: "/srv/app", context: "system_u:object_r:nfs_t:s0"},
		{name: "content type", path: "/srv/app", context: "system_u:object_r:httpd_sys_content_t:s0"},
		{name: "home path", path: "/home/user/app", context: "system_u:object_r:myapp_exec_t:s0"},
		{name: "root home path", path: "/root/app", context: "system_u:object_r:myapp_exec_t:s0"},
		{name: "temporary path", path: "/var/tmp/app", context: "system_u:object_r:myapp_exec_t:s0"},
		{name: "runtime path", path: "/run/app", context: "system_u:object_r:myapp_exec_t:s0"},
		{name: "shared memory path", path: "/dev/shm/app", context: "system_u:object_r:myapp_exec_t:s0"},
		{name: "unavailable context on risky path", path: "/run/user/1000/app", contextErr: errors.New("context unavailable")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			warning := selinuxInstallWarning(test.path, enforcePath, func(string) (string, error) {
				return test.context, test.contextErr
			})
			if warning == "" {
				t.Fatal("warning is empty")
			}
		})
	}
}

func TestSELinuxInstallWarningSkipsUncertainOrAllowedCases(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		enforce     string
		context     string
		contextErr  error
		missingFile bool
	}{
		{name: "permissive", path: "/home/user/app", enforce: "0\n", context: "unconfined_u:object_r:user_home_t:s0"},
		{name: "system label", path: "/usr/local/bin/app", enforce: "1\n", context: "system_u:object_r:usr_t:s0"},
		{name: "generic executable label", path: "/opt/app/bin/app", enforce: "1\n", context: "system_u:object_r:bin_t:s0"},
		{name: "custom executable label", path: "/opt/app/bin/app", enforce: "1\n", context: "system_u:object_r:myapp_exec_t:s0"},
		{name: "unknown context", path: "/usr/local/bin/app", enforce: "1\n", contextErr: errors.New("context unavailable")},
		{name: "similar path prefix", path: "/home2/user/app", enforce: "1\n", context: "system_u:object_r:myapp_exec_t:s0"},
		{name: "SELinux unavailable", path: "/home/user/app", missingFile: true, context: "unconfined_u:object_r:user_home_t:s0"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			enforcePath := filepath.Join(t.TempDir(), "enforce")
			if !test.missingFile {
				if err := os.WriteFile(enforcePath, []byte(test.enforce), 0644); err != nil {
					t.Fatal(err)
				}
			}
			warning := selinuxInstallWarning(test.path, enforcePath, func(string) (string, error) {
				return test.context, test.contextErr
			})
			if warning != "" {
				t.Fatalf("warning = %q, want none", warning)
			}
		})
	}
}

func TestInstallWarningTerminalDistinguishesNonterminalAndProbeFailure(t *testing.T) {
	input, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()

	interactive, err := installWarningTerminal(input, output)
	if err != nil {
		t.Fatalf("pipe terminal check error = %v", err)
	}
	if interactive {
		t.Fatal("pipe reported as interactive")
	}

	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := installWarningTerminal(input, output); err == nil {
		t.Fatal("closed input terminal probe returned no error")
	}
}
