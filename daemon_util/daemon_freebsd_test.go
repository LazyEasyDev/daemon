//go:build freebsd

package daemon_util

import (
	"strings"
	"testing"
)

func TestFreeBSDRespawnsWithoutRetryLimit(t *testing.T) {
	for _, setting := range []string{
		`command="/usr/sbin/daemon"`,
		`"$command" -R 30 -P "$pidfile" -f "$app_command"`,
	} {
		if !strings.Contains(defaultBSDConfig, setting) {
			t.Fatalf("FreeBSD config does not contain %q", setting)
		}
	}
	if strings.Contains(defaultBSDConfig, `/usr/sbin/daemon -p "$pidfile"`) {
		t.Fatal("FreeBSD config still tracks the child PID instead of the supervisor PID")
	}
}
