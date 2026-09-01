//go:build darwin

package daemon_util

import (
	"bytes"
	"html"
	"strings"
	"testing"
	"text/template"
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
