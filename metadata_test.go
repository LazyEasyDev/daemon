package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestServiceMetadataStoresResolvedApplicationPath(t *testing.T) {
	directory := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	target, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "current worker")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := executablePathFromTarget(link)
	if err != nil {
		t.Fatal(err)
	}

	metadataDirectory := filepath.Join(directory, "metadata")
	if err := writeServiceMetadataTo(metadataDirectory, "worker", resolved); err != nil {
		t.Fatal(err)
	}
	if got := readServiceMetadataFrom(metadataDirectory, "worker"); got != target {
		t.Fatalf("application path = %q, want resolved destination %q", got, target)
	}

	metadataPath := filepath.Join(metadataDirectory, "lz_lz_worker.json")
	content, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("read managed-name metadata file: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(content, &fields); err != nil {
		t.Fatalf("decode metadata file: %v", err)
	}
	if len(fields) != 1 || fields["application_path"] != target {
		t.Fatalf("metadata fields = %v, want only application_path", fields)
	}
}

func TestServiceMetadataOverwrite(t *testing.T) {
	directory := t.TempDir()
	if err := writeServiceMetadataTo(directory, "worker", "/opt/worker-v1"); err != nil {
		t.Fatal(err)
	}
	if err := writeServiceMetadataTo(directory, "worker", "/opt/worker-v2"); err != nil {
		t.Fatal(err)
	}
	if got := readServiceMetadataFrom(directory, "worker"); got != "/opt/worker-v2" {
		t.Fatalf("application path = %q, want overwritten path", got)
	}
}

func TestReadServiceMetadataReturnsEmptyForMissingOrMalformedFile(t *testing.T) {
	directory := t.TempDir()
	if got := readServiceMetadataFrom(directory, "worker"); got != "" {
		t.Fatalf("missing metadata path = %q, want empty", got)
	}

	path, err := serviceMetadataPath(directory, "worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := readServiceMetadataFrom(directory, "worker"); got != "" {
		t.Fatalf("malformed metadata path = %q, want empty", got)
	}
}

func TestRemoveServiceMetadataAllowsMissingFile(t *testing.T) {
	directory := t.TempDir()
	if err := writeServiceMetadataTo(directory, "worker", "/opt/worker"); err != nil {
		t.Fatal(err)
	}
	path, err := serviceMetadataPath(directory, "worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := removeServiceMetadataFrom(directory, "worker"); err != nil {
		t.Fatalf("remove missing metadata: %v", err)
	}
}
