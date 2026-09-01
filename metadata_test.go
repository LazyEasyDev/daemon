package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	if err := writeServiceMetadataTo(metadataDirectory, "worker", resolved, 45*time.Second); err != nil {
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
	if len(fields) != 2 || fields["application_path"] != target || fields["stop_timeout_seconds"] != float64(45) {
		t.Fatalf("metadata fields = %v, want application path and stop timeout", fields)
	}
	if got, ok := readServiceStopTimeoutFrom(metadataDirectory, "worker"); !ok || got != 45*time.Second {
		t.Fatalf("stop timeout = %v, %v; want 45s, true", got, ok)
	}
}

func TestServiceMetadataOverwrite(t *testing.T) {
	directory := t.TempDir()
	if err := writeServiceMetadataTo(directory, "worker", "/opt/worker-v1", 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := writeServiceMetadataTo(directory, "worker", "/opt/worker-v2", 60*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := readServiceMetadataFrom(directory, "worker"); got != "/opt/worker-v2" {
		t.Fatalf("application path = %q, want overwritten path", got)
	}
	if got, ok := readServiceStopTimeoutFrom(directory, "worker"); !ok || got != time.Minute {
		t.Fatalf("stop timeout = %v, %v; want 1m0s, true", got, ok)
	}
}

func TestReadServiceMetadataReturnsEmptyForMissingOrMalformedFile(t *testing.T) {
	directory := t.TempDir()
	if got := readServiceMetadataFrom(directory, "worker"); got != "" {
		t.Fatalf("missing metadata path = %q, want empty", got)
	}
	if got, ok := readServiceStopTimeoutFrom(directory, "worker"); ok || got != 0 {
		t.Fatalf("missing metadata timeout = %v, %v; want 0, false", got, ok)
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
	if got, ok := readServiceStopTimeoutFrom(directory, "worker"); ok || got != 0 {
		t.Fatalf("malformed metadata timeout = %v, %v; want 0, false", got, ok)
	}
}

func TestReadServiceStopTimeoutAllowsOlderMetadata(t *testing.T) {
	directory := t.TempDir()
	path, err := serviceMetadataPath(directory, "worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"application_path":"/opt/worker"}`), 0644); err != nil {
		t.Fatal(err)
	}

	if got := readServiceMetadataFrom(directory, "worker"); got != "/opt/worker" {
		t.Fatalf("application path = %q, want /opt/worker", got)
	}
	if got, ok := readServiceStopTimeoutFrom(directory, "worker"); ok || got != 0 {
		t.Fatalf("older metadata timeout = %v, %v; want 0, false", got, ok)
	}
}

func TestRemoveServiceMetadataAllowsMissingFile(t *testing.T) {
	directory := t.TempDir()
	if err := writeServiceMetadataTo(directory, "worker", "/opt/worker", 45*time.Second); err != nil {
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
