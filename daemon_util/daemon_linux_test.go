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

func TestDefaultTemplatesDoNotRequireLogDirectories(t *testing.T) {
	templates := map[string]string{
		"bobcat":  defaultBobCatConfig,
		"openrc":  defaultOpenRCConfig,
		"openwrt": defaultOpenWrtConfig,
		"systemd": defaultSystemDConfig,
		"systemv": defaultSystemVConfig,
		"upstart": defaultUpstartConfig,
	}

	for name, serviceTemplate := range templates {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(serviceTemplate, "/var/log") {
				t.Fatal("default service template requires /var/log")
			}
		})
	}
}

func TestOpenRCDetected(t *testing.T) {
	root := t.TempDir()
	if openRCDetected(root) {
		t.Fatal("openRCDetected() accepted an empty root")
	}
	if err := os.MkdirAll(filepath.Join(root, "run", "openrc"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sbin"), 0755); err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(root, "sbin", "openrc-run")
	if err := os.WriteFile(runner, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if openRCDetected(root) {
		t.Fatal("openRCDetected() accepted a non-executable runner")
	}
	if err := os.Chmod(runner, 0755); err != nil {
		t.Fatal(err)
	}
	if !openRCDetected(root) {
		t.Fatal("openRCDetected() rejected an active OpenRC root")
	}
}

func TestDefaultOpenRCConfigUsesNativeLifecycle(t *testing.T) {
	for _, required := range []string{
		"#!/sbin/openrc-run",
		"command_background=yes",
		`pidfile="/run/${RC_SVCNAME}.pid"`,
		`retry="TERM/12/KILL/5"`,
		"need localmount",
	} {
		if !strings.Contains(defaultOpenRCConfig, required) {
			t.Fatalf("default OpenRC template is missing %q", required)
		}
	}
}

func TestDefaultOpenRCConfigParsesAsShell(t *testing.T) {
	serviceTemplate, err := template.New("openRCConfig").Funcs(template.FuncMap{"shellQuote": shellQuote}).Parse(defaultOpenRCConfig)
	if err != nil {
		t.Fatal(err)
	}
	var rendered bytes.Buffer
	data := struct {
		Name, Description, Path, Args string
	}{
		Name:        "test-service",
		Description: "Test service's description",
		Path:        "/opt/test service/bin/app",
		Args:        shellQuoteArgs([]string{"value with spaces", "it's", "$HOME"}),
	}
	if err := serviceTemplate.Execute(&rendered, data); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", "-n")
	command.Stdin = &rendered
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("rendered OpenRC template does not parse as shell: %v\n%s", err, output)
	}
}

func TestDefaultBobCatConfigUsesOnlyPIDFileStop(t *testing.T) {
	for _, unsafe := range []string{"pidof", "kill -9"} {
		if strings.Contains(defaultBobCatConfig, unsafe) {
			t.Fatalf("default Bobcat template contains %q", unsafe)
		}
	}
	for _, required := range []string{
		`status)`,
		`is_running()`,
		`kill -0 "$pid"`,
		`--pidfile "$PIDFILE" --exec "$DAEMON"`,
		`while kill -0 "$pid"`,
		`sleep 1`,
		`sleep 12`,
	} {
		if !strings.Contains(defaultBobCatConfig, required) {
			t.Fatalf("default Bobcat template is missing %q", required)
		}
	}
}

func TestDefaultSystemDConfigQuotesMetadataWithoutPIDFile(t *testing.T) {
	if !strings.Contains(defaultSystemDConfig, `Description={{systemdConfigQuote .Description}}`) {
		t.Fatal("default systemd template does not quote its description")
	}
	if !strings.Contains(defaultSystemDConfig, "Type=exec") {
		t.Fatal("default systemd template does not wait for exec startup")
	}
	for _, obsolete := range []string{"PIDFile=", "ExecStartPre=/bin/rm"} {
		if strings.Contains(defaultSystemDConfig, obsolete) {
			t.Fatalf("default systemd template contains obsolete directive %q", obsolete)
		}
	}
}

func TestDefaultSystemVConfigChecksStartupAndStopBeforeRestart(t *testing.T) {
	for _, required := range []string{`[ -x "$exec" ]`, `kill -0 "$pid"`, `stop && start`} {
		if !strings.Contains(defaultSystemVConfig, required) {
			t.Fatalf("default System V template is missing %q", required)
		}
	}
	if strings.Contains(defaultSystemVConfig, `[ -x $exec ]`) {
		t.Fatal("default System V template contains an unquoted executable check")
	}
}

func TestDefaultOpenWrtConfigStopsBeforeRestart(t *testing.T) {
	if !strings.Contains(defaultOpenWrtConfig, `stop && start`) {
		t.Fatal("default OpenWrt template restarts after a failed stop")
	}
}
