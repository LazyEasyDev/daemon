//go:build linux

package daemon_util

import (
	"strings"
	"testing"
)

func TestDefaultTemplatesDoNotRequireLogDirectories(t *testing.T) {
	templates := map[string]string{
		"bobcat":  defaultBobCatConfig,
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
