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
	if !strings.Contains(warning, executablePath) || !strings.Contains(warning, "/opt") {
		t.Fatalf("warning = %q, want executable path and remediation", warning)
	}
}

func TestSELinuxInstallWarningSkipsUncertainOrAllowedCases(t *testing.T) {
	tests := []struct {
		name        string
		enforce     string
		context     string
		contextErr  error
		missingFile bool
	}{
		{name: "permissive", enforce: "0\n", context: "unconfined_u:object_r:user_home_t:s0"},
		{name: "system label", enforce: "1\n", context: "system_u:object_r:usr_t:s0"},
		{name: "unknown context", enforce: "1\n", contextErr: errors.New("context unavailable")},
		{name: "SELinux unavailable", missingFile: true, context: "unconfined_u:object_r:user_home_t:s0"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			enforcePath := filepath.Join(t.TempDir(), "enforce")
			if !test.missingFile {
				if err := os.WriteFile(enforcePath, []byte(test.enforce), 0644); err != nil {
					t.Fatal(err)
				}
			}
			warning := selinuxInstallWarning("/home/user/app", enforcePath, func(string) (string, error) {
				return test.context, test.contextErr
			})
			if warning != "" {
				t.Fatalf("warning = %q, want none", warning)
			}
		})
	}
}
