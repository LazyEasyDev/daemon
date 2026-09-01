//go:build darwin

package daemon_util

import (
	"bytes"
	"errors"
	"html"
	"strings"
	"testing"
	"text/template"
	"time"
)

func TestLaunchdTemplateConfiguresStopTimeout(t *testing.T) {
	data := struct {
		Name, Path, WorkingDirectory string
		Args                         []string
		StopTimeoutSeconds           int64
	}{"worker", "/opt/worker", "/opt", nil, 45}
	tpl, err := template.New("propertyList").Funcs(template.FuncMap{"xml": html.EscapeString}).Parse(defaultPropertyList)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := tpl.Execute(&output, data); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "<key>ExitTimeOut</key>\n\t<integer>45</integer>") {
		t.Fatal("rendered property list does not configure a 45-second ExitTimeOut")
	}
	if !strings.Contains(output.String(), "<key>WorkingDirectory</key>\n\t<string>/opt</string>") {
		t.Fatal("rendered property list does not configure the executable directory")
	}
}

func TestWaitForLaunchdStop(t *testing.T) {
	checks := 0
	err := waitForLaunchdStop(time.Second, time.Millisecond, func() (bool, error) {
		checks++
		return checks < 3, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checks != 3 {
		t.Fatalf("running checks = %d, want 3", checks)
	}
}

func TestWaitForLaunchdStopTimesOut(t *testing.T) {
	err := waitForLaunchdStop(10*time.Millisecond, time.Millisecond, func() (bool, error) {
		return true, nil
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want timeout", err)
	}
}

func TestWaitForLaunchdStopPropagatesQueryError(t *testing.T) {
	queryErr := errors.New("launchctl unavailable")
	err := waitForLaunchdStop(time.Second, time.Millisecond, func() (bool, error) {
		return false, queryErr
	})
	if !errors.Is(err, queryErr) {
		t.Fatalf("error = %v, want %v", err, queryErr)
	}
}

func TestLaunchdStatus(t *testing.T) {
	tests := []struct {
		name           string
		status         string
		exitCode       int
		wantRunning    bool
		wantRecognized bool
	}{
		{name: "loaded", status: "service = { pid = 42 }", wantRunning: true, wantRecognized: true},
		{name: "not loaded", status: `Could not find service "worker" in domain for user gui: 501`, exitCode: 113, wantRecognized: true},
		{name: "domain failure", status: "Operation not permitted", exitCode: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			running, recognized := launchdStatus(test.status, test.exitCode)
			if running != test.wantRunning || recognized != test.wantRecognized {
				t.Fatalf("launchdStatus() = (%v, %v), want (%v, %v)", running, recognized, test.wantRunning, test.wantRecognized)
			}
		})
	}
}
