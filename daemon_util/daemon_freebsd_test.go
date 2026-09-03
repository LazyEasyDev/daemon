//go:build freebsd

package daemon_util

import (
	"bytes"
	"strings"
	"testing"
	"text/template"
)

func renderFreeBSDConfig(t *testing.T, stopTimeoutSeconds int64) string {
	t.Helper()
	workingDirectory := "/opt/worker's files"
	data := struct {
		Name, RCName, RCVar, Description, Path, Args, WorkingDirectory string
		StopTimeoutSeconds                                             int64
	}{"worker", "worker", "worker_enable", "worker", workingDirectory + "/worker", "", workingDirectory, stopTimeoutSeconds}
	tpl, err := template.New("bsdConfig").Funcs(template.FuncMap{"shellQuote": shellQuote}).Parse(defaultBSDConfig)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := tpl.Execute(&output, data); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

func requireOrdered(t *testing.T, source string, fragments ...string) {
	t.Helper()
	remaining := source
	for _, fragment := range fragments {
		index := strings.Index(remaining, fragment)
		if index < 0 {
			t.Fatalf("rendered template does not contain %q in the required order", fragment)
		}
		remaining = remaining[index+len(fragment):]
	}
}

func TestFreeBSDRespawnsWithoutRetryLimit(t *testing.T) {
	for _, setting := range []string{
		`command="/usr/sbin/daemon"`,
		`"$command" -R 30 -P "$pidfile" -p "$child_pidfile" -f "$app_command"`,
	} {
		if !strings.Contains(defaultBSDConfig, setting) {
			t.Fatalf("FreeBSD config does not contain %q", setting)
		}
	}
	if strings.Contains(defaultBSDConfig, `/usr/sbin/daemon -p "$pidfile"`) {
		t.Fatal("FreeBSD config still tracks the child PID instead of the supervisor PID")
	}
}

func TestFreeBSDTemplateConfiguresStopTimeout(t *testing.T) {
	output := renderFreeBSDConfig(t, 45)
	for _, setting := range []string{
		"stop_timeout=45",
		`supervisor_pid=$(daemon_supervisor_pid)`,
		`child_pid=$(check_pidfile "$child_pidfile" "$app_command")`,
		`kill -TERM "$supervisor_pid"`,
		`kill -KILL "$child_pid"`,
	} {
		if !strings.Contains(output, setting) {
			t.Fatalf("rendered template does not contain %q", setting)
		}
	}
}

func TestFreeBSDTemplateConfiguresWorkingDirectory(t *testing.T) {
	output := renderFreeBSDConfig(t, 45)
	for _, setting := range []string{
		"app_directory=" + shellQuote("/opt/worker's files"),
		`cd "$app_directory" || return 1`,
	} {
		if !strings.Contains(output, setting) {
			t.Fatalf("rendered template does not contain %q", setting)
		}
	}
}

func TestFreeBSDStatusChecksLockedSupervisorPIDFile(t *testing.T) {
	output := renderFreeBSDConfig(t, 45)
	for _, setting := range []string{
		`status_cmd="daemon_status"`,
		`/usr/bin/pgrep -L -F "$pidfile" 2>/dev/null`,
		`supervisor_pid=$(daemon_supervisor_pid)`,
		`if [ -n "$supervisor_pid" ]; then`,
	} {
		if !strings.Contains(output, setting) {
			t.Fatalf("rendered template does not contain %q", setting)
		}
	}
}

func TestFreeBSDStopValidatesPIDsBeforeSignaling(t *testing.T) {
	output := renderFreeBSDConfig(t, 45)
	if strings.Contains(output, `supervisor_pid=$(cat "$pidfile")`) {
		t.Fatal("stop command trusts the raw supervisor PID file instead of rc.subr validation")
	}
	if strings.Contains(output, `child_pid=$(cat "$child_pidfile")`) {
		t.Fatal("stop command trusts the raw child PID file instead of rc.subr validation")
	}
	const childCheck = `child_pid=$(check_pidfile "$child_pidfile" "$app_command")`
	if count := strings.Count(output, childCheck); count != 3 {
		t.Fatalf("child PID validation count = %d, want 3", count)
	}
}

func TestFreeBSDStopOrdersEscalationAndCleanup(t *testing.T) {
	output := renderFreeBSDConfig(t, 45)
	requireOrdered(t, output,
		`kill -TERM "$supervisor_pid"`,
		`if [ "$elapsed" -ge "$stop_timeout" ]; then`,
		`child_pid=$(check_pidfile "$child_pidfile" "$app_command")`,
		`kill -KILL "$child_pid"`,
		`kill -KILL "$supervisor_pid"`,
		`force_elapsed=0`,
		`while [ "$force_elapsed" -lt 5 ]; do`,
		`child_pid=$(check_pidfile "$child_pidfile" "$app_command")`,
		`child_pid=$(check_pidfile "$child_pidfile" "$app_command")`,
		`if kill -0 "$supervisor_pid" 2>/dev/null || [ -n "$child_pid" ]; then`,
		`rm -f "$pidfile" "$child_pidfile"`,
	)
}

func TestFreeBSDStatus(t *testing.T) {
	tests := []struct {
		name           string
		status         string
		exitCode       int
		wantPID        string
		wantRunning    bool
		wantRecognized bool
	}{
		{name: "running", status: "worker is running as pid 42.", wantPID: "42", wantRunning: true, wantRecognized: true},
		{name: "stopped", status: "worker is not running.", exitCode: 1, wantRecognized: true},
		{name: "command failure", status: "service: not found", exitCode: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			running, recognized := freeBSDStatus(test.status, test.exitCode)
			if running != test.wantRunning || recognized != test.wantRecognized {
				t.Fatalf("freeBSDStatus() = (%v, %v), want (%v, %v)", running, recognized, test.wantRunning, test.wantRecognized)
			}
			if pid := freeBSDStatusPID(test.status); pid != test.wantPID {
				t.Fatalf("freeBSDStatusPID() = %q, want %q", pid, test.wantPID)
			}
		})
	}
}
