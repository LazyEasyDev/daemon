//go:build linux

package daemon_util

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
)

func TestLinuxTemplatesConfigureStopTimeout(t *testing.T) {
	data := struct {
		Name, Description, Dependencies, Path, Args string
		StopTimeoutSeconds                          int64
	}{"worker", "worker", "", "/opt/worker", "", 45}
	funcs := template.FuncMap{
		"shellQuote":         shellQuote,
		"systemdQuote":       systemdQuote,
		"systemdConfigQuote": systemdConfigQuote,
	}
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{name: "systemd", source: defaultSystemDConfig, want: "TimeoutStopSec=45s"},
		{name: "OpenRC", source: defaultOpenRCConfig, want: `retry="TERM/45/KILL/5"`},
		{name: "OpenWrt", source: defaultOpenWrtConfig, want: "procd_set_param term_timeout 45"},
		{name: "Upstart", source: defaultUpstartConfig, want: "kill timeout 45"},
		{name: "System V", source: defaultSystemVConfig, want: "stop_timeout=45"},
		{name: "Buildroot", source: defaultBuildrootConfig, want: "STOP_TIMEOUT=45"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tpl, err := template.New(test.name).Funcs(funcs).Parse(test.source)
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			if err := tpl.Execute(&output, data); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), test.want) {
				t.Fatalf("rendered template does not contain %q", test.want)
			}
		})
	}
}

func TestOpenWrtRespawnsWithoutRetryLimit(t *testing.T) {
	if !strings.Contains(defaultOpenWrtConfig, "procd_set_param respawn 0 30 0") {
		t.Fatal("OpenWrt config must respawn after 30 seconds without a retry limit")
	}
	if strings.Contains(defaultOpenWrtConfig, "procd_set_param respawn 0 30 10000") {
		t.Fatal("OpenWrt config still contains the old finite retry limit")
	}
}

func TestOpenRCRespawnsWithoutRetryLimit(t *testing.T) {
	for _, setting := range []string{
		"supervisor=supervise-daemon",
		"respawn_delay=30",
		"respawn_max=0",
	} {
		if !strings.Contains(defaultOpenRCConfig, setting) {
			t.Fatalf("OpenRC config does not contain %q", setting)
		}
	}
	if strings.Contains(defaultOpenRCConfig, "command_background=yes") {
		t.Fatal("OpenRC config must leave the application in the foreground")
	}
}

func TestUpstartStatusActive(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{status: "worker start/starting", want: true},
		{status: "worker start/pre-start, process 101", want: true},
		{status: "worker start/spawned, process 102", want: true},
		{status: "worker start/post-start, process 103", want: true},
		{status: "worker start/running, process 104", want: true},
		{status: "worker stop/stopping, process 104"},
		{status: "worker stop/waiting"},
		{status: "other start/running, process 104"},
	}

	for _, test := range tests {
		if got := upstartStatusActive("worker", test.status); got != test.want {
			t.Errorf("upstartStatusActive(%q) = %v, want %v", test.status, got, test.want)
		}
	}
}

func TestBuildrootValidatesProcessBeforeSignals(t *testing.T) {
	for _, command := range []string{
		`start-stop-daemon -K -t -q -p "$PIDFILE" -x "$DAEMON"`,
		`start-stop-daemon -K -q -s KILL -p "$PIDFILE" -x "$DAEMON"`,
	} {
		if !strings.Contains(defaultBuildrootConfig, command) {
			t.Fatalf("Buildroot config does not contain %q", command)
		}
	}
	for _, command := range []string{`kill -0 "$pid"`, `kill -KILL "$pid"`} {
		if strings.Contains(defaultBuildrootConfig, command) {
			t.Fatalf("Buildroot config still contains raw PID operation %q", command)
		}
	}
}

func TestSystemVValidatesProcessBeforeSignals(t *testing.T) {
	for _, command := range []string{
		`[ "$pid" -gt 1 ]`,
		`[ "$exec" -ef "/proc/$pid/exe" ]`,
		`tr '\000' '\n' < "/proc/$pid/cmdline" | grep -Fqx "$exec"`,
		`if ! is_expected_process; then`,
		`if is_expected_process && ! kill -KILL "$pid"`,
	} {
		if !strings.Contains(defaultSystemVConfig, command) {
			t.Fatalf("System V config does not contain %q", command)
		}
	}
	if strings.Contains(defaultSystemVConfig, `while kill -0 "$pid"`) {
		t.Fatal("System V stop loop still trusts raw PID liveness")
	}
}

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
