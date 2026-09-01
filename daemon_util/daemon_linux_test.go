//go:build linux

package daemon_util

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
)

func TestLinuxTemplatesConfigureStopTimeout(t *testing.T) {
	data := struct {
		Name, Description, Path, Args, WorkingDirectory string
		StopTimeoutSeconds                              int64
	}{"worker", "worker", "/opt/worker", "", "/opt", 45}
	funcs := template.FuncMap{
		"shellQuote":         shellQuote,
		"systemdQuote":       systemdQuote,
		"systemdConfigQuote": systemdConfigQuote,
		"upstartQuote":       upstartQuote,
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

func TestLinuxTemplatesConfigureWorkingDirectory(t *testing.T) {
	workingDirectory := "/opt/worker's files"
	data := struct {
		Name, Description, Path, Args, WorkingDirectory string
		StopTimeoutSeconds                              int64
	}{"worker", "worker", workingDirectory + "/worker", shellQuoteArgs([]string{"argument with spaces"}), workingDirectory, 45}
	funcs := template.FuncMap{
		"shellQuote":         shellQuote,
		"systemdQuote":       systemdQuote,
		"systemdConfigQuote": systemdConfigQuote,
		"upstartQuote":       upstartQuote,
	}
	tests := []struct {
		name   string
		source string
		wants  []string
	}{
		{name: "systemd", source: defaultSystemDConfig, wants: []string{"WorkingDirectory=" + systemdConfigQuote(workingDirectory)}},
		{name: "OpenRC", source: defaultOpenRCConfig, wants: []string{"directory=" + shellQuote(workingDirectory)}},
		{name: "OpenWrt", source: defaultOpenWrtConfig, wants: []string{
			"WORKING_DIRECTORY=" + shellQuote(workingDirectory),
			`procd_set_param command /bin/sh -c 'cd "$1" && shift && exec "$@"' sh "$WORKING_DIRECTORY" "$PROG" 'argument with spaces'`,
		}},
		{name: "Upstart", source: defaultUpstartConfig, wants: []string{"chdir " + upstartQuote(workingDirectory)}},
		{name: "System V", source: defaultSystemVConfig, wants: []string{
			"working_directory=" + shellQuote(workingDirectory),
			`cd "$working_directory" || exit 5`,
		}},
		{name: "Buildroot", source: defaultBuildrootConfig, wants: []string{
			"WORKING_DIRECTORY=" + shellQuote(workingDirectory),
			`if ! cd "$WORKING_DIRECTORY"; then`,
		}},
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
			for _, want := range test.wants {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("rendered template does not contain %q", want)
				}
			}
		})
	}
}

func TestOpenWrtLauncherPreservesWorkingDirectoryAndArguments(t *testing.T) {
	const launchScript = `cd "$1" && shift && exec "$@"`
	if !strings.Contains(defaultOpenWrtConfig, shellQuote(launchScript)) {
		t.Fatal("OpenWrt config does not contain the tested positional launcher")
	}

	workingDirectory := t.TempDir()
	arguments := []string{"", "argument with spaces", "argument's quote", `literal;$(not-run)`, "%value", "-leading"}
	inspectScript := `printf 'cwd=<%s>\ncount=<%s>\n' "$PWD" "$#"; for argument do printf 'arg=<%s>\n' "$argument"; done`
	commandArguments := []string{"-c", launchScript, "sh", workingDirectory, "/bin/sh", "-c", inspectScript, "app"}
	commandArguments = append(commandArguments, arguments...)
	output, err := exec.Command("/bin/sh", commandArguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("run OpenWrt launcher: %v\n%s", err, output)
	}

	want := "cwd=<" + workingDirectory + ">\ncount=<6>\n"
	for _, argument := range arguments {
		want += "arg=<" + argument + ">\n"
	}
	if string(output) != want {
		t.Fatalf("launcher output = %q, want %q", output, want)
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
		"stopgroup=true",
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

func TestUpstartRespawnsWithoutRetryLimit(t *testing.T) {
	if !strings.Contains(defaultUpstartConfig, "respawn limit 0 5") {
		t.Fatal("Upstart config must respawn without a retry limit")
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

func TestSystemDStatusActive(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{status: "active", want: true},
		{status: "activating", want: true},
		{status: "reloading", want: true},
		{status: "refreshing", want: true},
		{status: "inactive"},
		{status: "failed"},
		{status: "deactivating"},
	}

	for _, test := range tests {
		if got := systemDStatusActive(test.status); got != test.want {
			t.Errorf("systemDStatusActive(%q) = %v, want %v", test.status, got, test.want)
		}
	}
}

func TestSystemDStopsControlGroup(t *testing.T) {
	if !strings.Contains(defaultSystemDConfig, "KillMode=control-group") {
		t.Fatal("systemd config must stop the entire service control group")
	}
}

func TestOpenWrtStatusActive(t *testing.T) {
	tests := []struct {
		status   string
		exitCode int
		want     bool
	}{
		{status: "running", want: true},
		{status: "running (1/2)", want: true},
		{status: "not running", exitCode: 5},
		{status: "inactive", exitCode: 3},
		{status: "not running", exitCode: 1},
		{status: "unknown instance", exitCode: 4},
	}

	for _, test := range tests {
		if got := openWrtStatusActive(test.status, test.exitCode); got != test.want {
			t.Errorf("openWrtStatusActive(%q, %d) = %v, want %v", test.status, test.exitCode, got, test.want)
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
		`if ! is_expected_process; then`,
		`setsid "$exec"`,
		`kill -TERM -- "-$pid"`,
		`if is_process_group_running && ! kill -KILL -- "-$pid"`,
		`while is_process_group_running && [ "$elapsed" -lt "$stop_timeout" ]`,
	} {
		if !strings.Contains(defaultSystemVConfig, command) {
			t.Fatalf("System V config does not contain %q", command)
		}
	}
	if strings.Contains(defaultSystemVConfig, `while kill -0 "$pid"`) {
		t.Fatal("System V stop loop still trusts raw PID liveness")
	}
	if strings.Contains(defaultSystemVConfig, `kill -KILL "$pid"`) {
		t.Fatal("System V stop still kills only the main process")
	}
	if strings.Contains(defaultSystemVConfig, `/proc/$pid/cmdline`) {
		t.Fatal("System V config still contains script-specific process matching")
	}
}

func TestSystemVStatusDoesNotRequireRedHatHelpers(t *testing.T) {
	for _, setting := range []string{
		`service_status() {`,
		`printf '%s (pid  %s) is running...\n' "$proc" "$pid"`,
		`service_status >/dev/null 2>&1 && exit 0`,
		`service_status >/dev/null 2>&1 || exit 0`,
	} {
		if !strings.Contains(defaultSystemVConfig, setting) {
			t.Fatalf("System V config does not contain %q", setting)
		}
	}
	for _, dependency := range []string{
		`/etc/rc.d/init.d/functions`,
		`status -p $pidfile $proc`,
		"\tsuccess\n",
		"\tfailure\n",
		`$"`,
	} {
		if strings.Contains(defaultSystemVConfig, dependency) {
			t.Fatalf("System V config still depends on %q", dependency)
		}
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
