//go:build darwin || freebsd || linux

package daemon_util

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveExecutablePathAcceptsNativeExecutableAndSymlink(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveExecutablePath("unused", executable)
	if err != nil {
		t.Fatalf("resolveExecutablePath(native executable) error = %v", err)
	}
	if resolved != executable {
		t.Fatalf("resolved native executable = %q, want %q", resolved, executable)
	}

	link := filepath.Join(t.TempDir(), "executable-link")
	if err := os.Symlink(executable, link); err != nil {
		t.Fatal(err)
	}
	resolved, err = resolveExecutablePath("unused", link)
	if err != nil {
		t.Fatalf("resolveExecutablePath(symlink) error = %v", err)
	}
	if resolved != link {
		t.Fatalf("resolved symlink = %q, want original path %q", resolved, link)
	}
}

func TestResolveExecutablePathRejectsDirectScript(t *testing.T) {
	script := filepath.Join(t.TempDir(), "worker.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveExecutablePath("unused", script); !errors.Is(err, ErrInvalidExecutablePath) {
		t.Fatalf("resolveExecutablePath(script) error = %v, want ErrInvalidExecutablePath", err)
	}
}

func TestResolveExecutablePathOnlyChecksNativeMagic(t *testing.T) {
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
	if _, err := resolveExecutablePath("unused", truncated); err != nil {
		t.Fatalf("resolveExecutablePath(magic-only file) error = %v", err)
	}
}
