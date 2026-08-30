//go:build darwin || freebsd || linux

package daemon_util

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateExecutable(t *testing.T) {
	directory := t.TempDir()
	if err := validateExecutable(directory); !errors.Is(err, ErrInvalidExecutablePath) {
		t.Fatalf("validateExecutable(directory) error = %v, want %v", err, ErrInvalidExecutablePath)
	}

	path := filepath.Join(directory, "service")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateExecutable(path); !errors.Is(err, ErrInvalidExecutablePath) {
		t.Fatalf("validateExecutable(non-executable file) error = %v, want %v", err, ErrInvalidExecutablePath)
	}
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := validateExecutable(path); err != nil {
		t.Fatalf("validateExecutable(executable file): %v", err)
	}
}

func TestCreateServiceLinksPreservesExistingPathsOnFailure(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "service")
	firstLink := filepath.Join(directory, "first")
	existingPath := filepath.Join(directory, "existing")
	unattemptedLink := filepath.Join(directory, "unattempted")

	if err := os.WriteFile(target, []byte("service"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existingPath, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}

	err := createServiceLinks(target, []string{firstLink, existingPath, unattemptedLink})
	if err == nil {
		t.Fatal("createServiceLinks() unexpectedly succeeded")
	}
	if _, err := os.Lstat(firstLink); !os.IsNotExist(err) {
		t.Fatalf("created link was not rolled back: %v", err)
	}
	content, err := os.ReadFile(existingPath)
	if err != nil {
		t.Fatalf("pre-existing path was removed: %v", err)
	}
	if string(content) != "keep" {
		t.Fatalf("pre-existing path changed to %q", content)
	}
	if _, err := os.Lstat(unattemptedLink); !os.IsNotExist(err) {
		t.Fatalf("later link was unexpectedly created: %v", err)
	}
}
