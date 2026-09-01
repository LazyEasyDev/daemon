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
	executable, err = filepath.EvalSymlinks(executable)
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
	if resolved != executable {
		t.Fatalf("resolved symlink = %q, want destination %q", resolved, executable)
	}
}

func TestResolveExecutablePathRejectsInvalidSymlink(t *testing.T) {
	directory := t.TempDir()
	dangling := filepath.Join(directory, "dangling")
	if err := os.Symlink(filepath.Join(directory, "missing"), dangling); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveExecutablePath(dangling); err == nil {
		t.Fatal("ResolveExecutablePath accepted a dangling symlink")
	}

	first := filepath.Join(directory, "first")
	second := filepath.Join(directory, "second")
	if err := os.Symlink(second, first); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(first, second); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveExecutablePath(first); err == nil {
		t.Fatal("ResolveExecutablePath accepted a symlink loop")
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
