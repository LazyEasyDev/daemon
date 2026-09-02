//go:build linux

package daemon_util

import (
	"bytes"
	"errors"
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
		"systemdDescription": systemdDescription,
		"systemdPathValue":   systemdPathValue,
		"upstartQuote":       upstartQuote,
	}
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{name: "systemd", source: defaultSystemDConfig, want: "TimeoutStopSec=45s"},
		{name: "OpenRC", source: defaultOpenRCConfig, want: `retry="TERM/45/KILL/5"`},
		{name: "OpenWrt", source: defaultOpenWrtConfig, want: "STOP_TIMEOUT=45"},
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
	funcs := template.FuncMap{
		"shellQuote":         shellQuote,
		"systemdQuote":       systemdQuote,
		"systemdDescription": systemdDescription,
		"systemdPathValue":   systemdPathValue,
		"upstartQuote":       upstartQuote,
	}
	tests := []struct {
		name             string
		source           string
		workingDirectory string
		wants            []string
	}{
		{name: "systemd", source: defaultSystemDConfig, workingDirectory: "/opt/worker % files", wants: []string{
			"Description=worker",
			"ExecStart=" + systemdQuote("/opt/worker % files/worker"),
			"WorkingDirectory=/opt/worker %% files",
		}},
		{name: "OpenRC", source: defaultOpenRCConfig, workingDirectory: "/opt/worker's % files", wants: []string{"directory=" + shellQuote("/opt/worker's % files")}},
		{name: "OpenWrt", source: defaultOpenWrtConfig, workingDirectory: "/opt/worker's % files", wants: []string{
			"WORKING_DIRECTORY=" + shellQuote("/opt/worker's % files"),
			`procd_set_param command /bin/sh -c 'cd "$1" && shift && exec "$@"' sh "$WORKING_DIRECTORY" "$PROG" 'argument with spaces'`,
		}},
		{name: "Upstart", source: defaultUpstartConfig, workingDirectory: "/opt/worker's % files", wants: []string{"chdir " + upstartQuote("/opt/worker's % files")}},
		{name: "System V", source: defaultSystemVConfig, workingDirectory: "/opt/worker's % files", wants: []string{
			"working_directory=" + shellQuote("/opt/worker's % files"),
			`cd "$working_directory" || exit 5`,
		}},
		{name: "Buildroot", source: defaultBuildrootConfig, workingDirectory: "/opt/worker's % files", wants: []string{
			"WORKING_DIRECTORY=" + shellQuote("/opt/worker's % files"),
			`if ! cd "$WORKING_DIRECTORY"; then`,
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := struct {
				Name, Description, Path, Args, WorkingDirectory string
				StopTimeoutSeconds                              int64
			}{"worker", "worker", test.workingDirectory + "/worker", shellQuoteArgs([]string{"argument with spaces"}), test.workingDirectory, 45}
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

func TestSystemDPathValueEscapesSpecifiers(t *testing.T) {
	got := systemdPathValue("/opt/worker % files")
	want := "/opt/worker %% files"
	if got != want {
		t.Fatalf("systemdPathValue() = %q, want %q", got, want)
	}
}

func TestSystemDDescriptionPreservesTextAndEscapesSpecifiers(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "plain", value: "worker service", want: "worker service"},
		{name: "specifier", value: "worker 100% service", want: "worker 100%% service"},
		{name: "quotes and backslash", value: `worker "quoted" \ path`, want: `worker "quoted" \ path`},
		{name: "trailing backslash", value: `worker\`, want: "worker\\ "},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := systemdDescription(test.value); got != test.want {
				t.Fatalf("systemdDescription() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestValidateSystemDExecutablePath(t *testing.T) {
	if err := validateSystemDExecutablePath("/opt/worker % files/worker"); err != nil {
		t.Fatalf("safe path rejected: %v", err)
	}

	for _, path := range []string{
		"/opt/worker$release/worker",
		"/opt/worker's/worker",
		`/opt/worker"quote/worker`,
		`/opt/worker\config/worker`,
		"/opt/worker\nExecStart=/bin/false",
		"/opt/worker\tconfig/worker",
		"/opt/worker\x7fconfig/worker",
		"/opt/worker /worker",
	} {
		if err := validateSystemDExecutablePath(path); !errors.Is(err, ErrInvalidExecutablePath) {
			t.Errorf("validateSystemDExecutablePath(%q) error = %v, want ErrInvalidExecutablePath", path, err)
		}
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

func TestOpenWrtWaitsForServiceStop(t *testing.T) {
	for _, setting := range []string{
		`procd_set_param term_timeout "$STOP_TIMEOUT"`,
		`service_stopped() {`,
		`while procd_running "$DAEMON"; do`,
		`if [ "$elapsed" -ge $((STOP_TIMEOUT + 5)) ]; then`,
		`return 1`,
	} {
		if !strings.Contains(defaultOpenWrtConfig, setting) {
			t.Fatalf("OpenWrt config does not contain %q", setting)
		}
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

func TestOpenRCStatus(t *testing.T) {
	tests := []struct {
		name           string
		status         string
		exitCode       int
		wantState      openRCServiceState
		wantRunning    bool
		wantStartable  bool
		wantStoppable  bool
		wantRecognized bool
	}{
		{name: "running", status: "status: started", wantState: openRCServiceStarted, wantRunning: true, wantStoppable: true, wantRecognized: true},
		{name: "stopped", status: "status: stopped", exitCode: 3, wantState: openRCServiceStopped, wantStartable: true, wantRecognized: true},
		{name: "stopping", status: "status: stopping", exitCode: 4, wantState: openRCServiceStopping, wantRunning: true, wantStoppable: true, wantRecognized: true},
		{name: "starting", status: "status: starting", exitCode: 8, wantState: openRCServiceStarting, wantRunning: true, wantStoppable: true, wantRecognized: true},
		{name: "inactive", status: "status: inactive", exitCode: 16, wantState: openRCServiceInactive, wantStartable: true, wantStoppable: true, wantRecognized: true},
		{name: "crashed", status: "status: crashed", exitCode: 32, wantState: openRCServiceCrashed, wantStoppable: true, wantRecognized: true},
		{name: "unsupervised", status: "status: unsupervised", exitCode: 64, wantState: openRCServiceUnsupervised, wantStoppable: true, wantRecognized: true},
		{name: "query failure", status: "permission denied", exitCode: 1},
		{name: "mismatched output", status: "status: stopped", exitCode: 32},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, recognized := openRCStatus(test.status, test.exitCode)
			if state != test.wantState || state.running() != test.wantRunning || state.startable() != test.wantStartable || state.stoppable() != test.wantStoppable || recognized != test.wantRecognized {
				t.Fatalf("openRCStatus() = (%v, %v, %v, %v, %v), want (%v, %v, %v, %v, %v)", state, state.running(), state.startable(), state.stoppable(), recognized, test.wantState, test.wantRunning, test.wantStartable, test.wantStoppable, test.wantRecognized)
			}
		})
	}
}

func TestUpstartRespawnsWithoutRetryLimit(t *testing.T) {
	if !strings.Contains(defaultUpstartConfig, "respawn limit 0 5") {
		t.Fatal("Upstart config must respawn without a retry limit")
	}
}

func TestUpstartStatus(t *testing.T) {
	tests := []struct {
		status         string
		exitCode       int
		wantRunning    bool
		wantRecognized bool
	}{
		{status: "worker start/starting", wantRunning: true, wantRecognized: true},
		{status: "worker start/pre-start, process 101", wantRunning: true, wantRecognized: true},
		{status: "worker start/spawned, process 102", wantRunning: true, wantRecognized: true},
		{status: "worker start/post-start, process 103", wantRunning: true, wantRecognized: true},
		{status: "worker start/running, process 104", wantRunning: true, wantRecognized: true},
		{status: "worker stop/stopping, process 104", wantRunning: true, wantRecognized: true},
		{status: "worker stop/waiting", wantRecognized: true},
		{status: "other start/running, process 104"},
		{status: "worker stop/waiting", exitCode: 1},
	}

	for _, test := range tests {
		gotRunning, gotRecognized := upstartStatus("worker", test.status, test.exitCode)
		if gotRunning != test.wantRunning || gotRecognized != test.wantRecognized {
			t.Errorf("upstartStatus(%q, %d) = (%v, %v), want (%v, %v)", test.status, test.exitCode, gotRunning, gotRecognized, test.wantRunning, test.wantRecognized)
		}
	}
}

func TestGeneratedInitStatus(t *testing.T) {
	tests := []struct {
		name           string
		status         string
		exitCode       int
		wantRunning    bool
		wantRecognized bool
	}{
		{name: "Buildroot running", status: "worker is running (pid 42)", wantRunning: true, wantRecognized: true},
		{name: "Buildroot stopped", status: "worker is stopped", exitCode: 3, wantRecognized: true},
		{name: "Buildroot failure", status: "permission denied", exitCode: 1},
		{name: "System V running", status: "worker (pid  42) is running...", wantRunning: true, wantRecognized: true},
		{name: "System V stopped", status: "worker is stopped", exitCode: 3, wantRecognized: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var running, recognized bool
			if strings.HasPrefix(test.name, "Buildroot") {
				running, recognized = buildrootStatus(test.status, test.exitCode)
			} else {
				running, recognized = systemVStatus("worker", test.status, test.exitCode)
			}
			if running != test.wantRunning || recognized != test.wantRecognized {
				t.Fatalf("status = (%v, %v), want (%v, %v)", running, recognized, test.wantRunning, test.wantRecognized)
			}
		})
	}
}

func TestSystemDStatus(t *testing.T) {
	tests := []struct {
		status         string
		wantRunning    bool
		wantRecognized bool
	}{
		{status: "active", wantRunning: true, wantRecognized: true},
		{status: "activating", wantRunning: true, wantRecognized: true},
		{status: "reloading", wantRunning: true, wantRecognized: true},
		{status: "refreshing", wantRunning: true, wantRecognized: true},
		{status: "inactive", wantRecognized: true},
		{status: "failed", wantRecognized: true},
		{status: "deactivating", wantRunning: true, wantRecognized: true},
		{status: "access denied"},
	}

	for _, test := range tests {
		gotRunning, gotRecognized := systemDStatus(test.status)
		if gotRunning != test.wantRunning || gotRecognized != test.wantRecognized {
			t.Errorf("systemDStatus(%q) = (%v, %v), want (%v, %v)", test.status, gotRunning, gotRecognized, test.wantRunning, test.wantRecognized)
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
		status         string
		exitCode       int
		wantState      openWrtServiceState
		wantRunning    bool
		wantStartable  bool
		wantStoppable  bool
		wantRecognized bool
	}{
		{status: "running", wantState: openWrtServiceRunning, wantRunning: true, wantStoppable: true, wantRecognized: true},
		{status: "running (1/2)", wantState: openWrtServiceRunning, wantRunning: true, wantStoppable: true, wantRecognized: true},
		{status: "active with no instances", wantState: openWrtServiceRunning, wantRunning: true, wantStoppable: true, wantRecognized: true},
		{status: "inactive", exitCode: 3, wantState: openWrtServiceInactive, wantStartable: true, wantRecognized: true},
		{status: "not running", exitCode: 5, wantState: openWrtServiceNotRunning, wantStartable: true, wantStoppable: true, wantRecognized: true},
		{status: "not running", exitCode: 3},
		{status: "not running", exitCode: 1},
		{status: "unknown instance", exitCode: 4},
	}

	for _, test := range tests {
		gotState, gotRecognized := openWrtStatus(test.status, test.exitCode)
		if gotState != test.wantState || gotState.running() != test.wantRunning || gotState.startable() != test.wantStartable || gotState.stoppable() != test.wantStoppable || gotRecognized != test.wantRecognized {
			t.Errorf("openWrtStatus(%q, %d) = (%v, %v, %v, %v, %v), want (%v, %v, %v, %v, %v)", test.status, test.exitCode, gotState, gotState.running(), gotState.startable(), gotState.stoppable(), gotRecognized, test.wantState, test.wantRunning, test.wantStartable, test.wantStoppable, test.wantRecognized)
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

func TestSystemVServiceLinksUseExistingRunlevelDirectories(t *testing.T) {
	root := t.TempDir()
	for _, runlevel := range []string{"2", "5", "6"} {
		if err := os.MkdirAll(filepath.Join(root, "etc", "rc"+runlevel+".d"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "rc3.d"), nil, 0644); err != nil {
		t.Fatal(err)
	}

	links, hasStartLink := existingSystemVServiceLinks(root, "worker")
	want := []string{
		filepath.Join(root, "etc", "rc2.d", "S87worker"),
		filepath.Join(root, "etc", "rc5.d", "S87worker"),
		filepath.Join(root, "etc", "rc6.d", "K17worker"),
	}
	if strings.Join(links, "\n") != strings.Join(want, "\n") {
		t.Fatalf("systemVServiceLinks() = %q, want %q", links, want)
	}
	if !hasStartLink {
		t.Fatal("systemVServiceLinks() did not report an existing start runlevel")
	}
}

func TestSystemVServiceLinksRequireStartRunlevel(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc", "rc0.d"), 0755); err != nil {
		t.Fatal(err)
	}

	links, hasStartLink := existingSystemVServiceLinks(root, "worker")
	want := []string{filepath.Join(root, "etc", "rc0.d", "K17worker")}
	if strings.Join(links, "\n") != strings.Join(want, "\n") {
		t.Fatalf("systemVServiceLinks() = %q, want %q", links, want)
	}
	if hasStartLink {
		t.Fatal("systemVServiceLinks() reported a missing start runlevel")
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
