//go:build darwin

package daemon_util

import (
	"fmt"
	"os"
	"testing"
)

func TestLaunchDomain(t *testing.T) {
	tests := []struct {
		kind Kind
		want string
	}{
		{kind: UserAgent, want: fmt.Sprintf("gui/%d", os.Getuid())},
		{kind: GlobalDaemon, want: "system"},
	}

	for _, test := range tests {
		record := &darwinRecord{name: "worker", kind: test.kind}
		if got, err := record.launchDomain(); err != nil || got != test.want {
			t.Fatalf("launchDomain(%s) = %q, %v; want %q", test.kind, got, err, test.want)
		}
		if got, err := record.launchTarget(); err != nil || got != test.want+"/worker" {
			t.Fatalf("launchTarget(%s) = %q, %v; want %q", test.kind, got, err, test.want+"/worker")
		}
	}
}
