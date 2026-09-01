//go:build darwin

package daemon_util

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveExecutablePathRejectsTruncatedNativeExecutable(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	magic, err := readExecutableMagic(executable)
	if err != nil {
		t.Fatal(err)
	}

	truncated := filepath.Join(t.TempDir(), "truncated-executable")
	if err := os.WriteFile(truncated, magic[:], 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveExecutablePath("unused", truncated); !errors.Is(err, ErrInvalidExecutablePath) {
		t.Fatalf("resolveExecutablePath(magic-only file) error = %v, want ErrInvalidExecutablePath", err)
	}
}
