//go:build linux

package daemon_util

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSystemVDetected(t *testing.T) {
	tests := []struct {
		name     string
		initPath string
		want     bool
	}{
		{
			name:     "init directory",
			initPath: "directory",
			want:     true,
		},
		{
			name: "missing init directory",
		},
		{
			name:     "init path is a file",
			initPath: "file",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			switch test.initPath {
			case "directory":
				if err := os.MkdirAll(filepath.Join(root, "etc/init.d"), 0755); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.MkdirAll(filepath.Join(root, "etc"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "etc/init.d"), nil, 0755); err != nil {
					t.Fatal(err)
				}
			}

			if got := systemVDetected(root); got != test.want {
				t.Fatalf("systemVDetected() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestBuildrootStyleInitDetected(t *testing.T) {
	tests := []struct {
		name       string
		rcS        string
		helperMode os.FileMode
		want       bool
	}{
		{
			name:       "direct init scan with daemon helper",
			rcS:        "#!/bin/sh\nfor script in /etc/init.d/S??*; do\n\t$script start\ndone\n",
			helperMode: 0755,
			want:       true,
		},
		{
			name:       "sysv runlevel scan",
			rcS:        "#!/bin/sh\nfor script in /etc/rcS.d/S??*; do\n\t$script start\ndone\n",
			helperMode: 0755,
			want:       false,
		},
		{
			name:       "missing daemon helper",
			rcS:        "#!/bin/sh\nfor script in /etc/init.d/S??*; do\n\t$script start\ndone\n",
			helperMode: 0,
			want:       false,
		},
		{
			name:       "non-executable daemon helper",
			rcS:        "#!/bin/sh\nfor script in /etc/init.d/S??*; do\n\t$script start\ndone\n",
			helperMode: 0644,
			want:       false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			initDirectory := filepath.Join(root, "etc/init.d")
			if err := os.MkdirAll(initDirectory, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(initDirectory, "rcS"), []byte(test.rcS), 0755); err != nil {
				t.Fatal(err)
			}

			if test.helperMode != 0 {
				helperDirectory := filepath.Join(root, "sbin")
				if err := os.MkdirAll(helperDirectory, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(helperDirectory, "start-stop-daemon"), nil, test.helperMode); err != nil {
					t.Fatal(err)
				}
			}

			if got := buildrootStyleInitDetected(root); got != test.want {
				t.Fatalf("buildrootStyleInitDetected() = %v, want %v", got, test.want)
			}
		})
	}
}
