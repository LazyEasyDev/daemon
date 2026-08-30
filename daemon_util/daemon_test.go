package daemon_util

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestResolveExecutablePathUsesDaemonDirectory(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	path, err := resolveExecutablePath("service-name", "")
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(filepath.Dir(executable), "service-name")
	if path != want {
		t.Fatalf("resolveExecutablePath() = %q, want %q", path, want)
	}
}

func TestResolveExecutablePathUsesConfiguredPath(t *testing.T) {
	configuredPath := filepath.Join(string(filepath.Separator), "opt", "apps", "myapp")
	path, err := resolveExecutablePath("myapp", configuredPath)
	if err != nil {
		t.Fatal(err)
	}
	if path != configuredPath {
		t.Fatalf("resolveExecutablePath() = %q, want %q", path, configuredPath)
	}
}

func TestShellQuoteArgs(t *testing.T) {
	args := []string{"--port", "8080", "value with spaces", "", "it's", "$HOME", `C:\apps\myapp`, "line one\nline two"}
	want := "'--port' '8080' 'value with spaces' '' 'it'\"'\"'s' '$HOME' 'C:\\apps\\myapp' 'line one\nline two'"
	if got := shellQuoteArgs(args); got != want {
		t.Fatalf("shellQuoteArgs() = %q, want %q", got, want)
	}
}

func TestShellQuoteArgsRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell is not available on Windows")
	}

	args := []string{"--port", "8080", "value with spaces", "", "it's", "$HOME", `C:\apps\myapp`, "line one\nline two"}
	script := "set -- " + shellQuoteArgs(args) + `; for arg do printf '%s\000' "$arg"; done`
	output, err := exec.Command("sh", "-c", script).Output()
	if err != nil {
		t.Fatal(err)
	}
	got := bytes.Split(bytes.TrimSuffix(output, []byte{0}), []byte{0})
	gotArgs := make([]string, len(got))
	for index := range got {
		gotArgs[index] = string(got[index])
	}
	if !slices.Equal(gotArgs, args) {
		t.Fatalf("shell argument round trip = %q, want %q", gotArgs, args)
	}
}

func TestSystemdQuoteArgs(t *testing.T) {
	args := []string{"--port", "value with spaces", "", "$HOME", "100%", `say "hi"`, `C:\apps\myapp`, "line one\nline two"}
	want := `"--port" "value with spaces" "" "$$HOME" "100%%" "say \"hi\"" "C:\\apps\\myapp" "line one\nline two"`
	if got := systemdQuoteArgs(args); got != want {
		t.Fatalf("systemdQuoteArgs() = %q, want %q", got, want)
	}
}

func TestNewRejectsUnsafeMetadata(t *testing.T) {
	kind := SystemDaemon
	if runtime.GOOS == "darwin" {
		kind = UserAgent
	}

	tests := []struct {
		name        string
		description string
		wantErr     error
	}{
		{name: "", wantErr: ErrInvalidName},
		{name: "../service", wantErr: ErrInvalidName},
		{name: "service/name", wantErr: ErrInvalidName},
		{name: "service", description: "line one\nline two"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(test.name, test.description, kind)
			if err == nil {
				t.Fatal("New() returned no error")
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("New() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestNewWithExecutableRequiresAbsolutePath(t *testing.T) {
	kind := SystemDaemon
	if runtime.GOOS == "darwin" {
		kind = UserAgent
	}

	if _, err := NewWithExecutable("myapp", "myapp", "relative/myapp", kind); !errors.Is(err, ErrInvalidExecutablePath) {
		t.Fatalf("NewWithExecutable() error = %v, want %v", err, ErrInvalidExecutablePath)
	}

	absolutePath := filepath.Join(string(filepath.Separator), "opt", "apps", "myapp")
	if _, err := NewWithExecutable("myapp", "myapp", absolutePath, kind); err != nil {
		t.Fatalf("NewWithExecutable() with absolute path: %v", err)
	}
}

func TestWriteTemplateFileIsAtomic(t *testing.T) {
	target := filepath.Join(t.TempDir(), "service.conf")

	err := writeTemplateFile(target, "invalid", "{{", nil, nil, 0644)
	if err == nil {
		t.Fatal("writeTemplateFile() returned no parse error")
	}
	if _, statErr := os.Stat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed render left target file: %v", statErr)
	}

	data := struct{ Name string }{Name: "example"}
	if err := writeTemplateFile(target, "valid", "name={{.Name}}", nil, data, 0600); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "name=example" {
		t.Fatalf("rendered content = %q", content)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("rendered mode = %o, want 600", info.Mode().Perm())
	}
}

func TestTemplateIsInstanceLocal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not support service templates")
	}

	kind := SystemDaemon
	if runtime.GOOS == "darwin" {
		kind = UserAgent
	}
	first, err := New("first", "first", kind)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New("second", "second", kind)
	if err != nil {
		t.Fatal(err)
	}
	original := second.GetTemplate()
	if err := first.SetTemplate("custom template"); err != nil {
		t.Fatal(err)
	}
	if second.GetTemplate() != original {
		t.Fatal("SetTemplate changed another daemon instance")
	}
}

func TestManagedServiceName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "worker", want: "lz_lz_worker"},
		{name: "my-worker", want: "lz_lz_my-worker"},
	}

	for _, test := range tests {
		got, err := ManagedServiceName(test.name)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("ManagedServiceName(%q) = %q, want %q", test.name, got, test.want)
		}
	}
}

func TestManagedServiceNameRejectsReservedPrefix(t *testing.T) {
	if _, err := ManagedServiceName("lz_lz_worker"); err == nil {
		t.Fatal("ManagedServiceName() accepted the reserved prefix")
	}
}

func TestManagedServiceNameRejectsWhitespace(t *testing.T) {
	if _, err := ManagedServiceName("my worker"); err == nil {
		t.Fatal("ManagedServiceName() accepted whitespace")
	}
}

func TestListServiceFilesFiltersSortsAndDeduplicates(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	for _, path := range []string{
		filepath.Join(first, "lz_lz_worker.service"),
		filepath.Join(first, "unmanaged.service"),
		filepath.Join(first, "lz_lz_wrong.conf"),
		filepath.Join(second, "S90lz_lz_api"),
		filepath.Join(second, "S90lz_lz_worker"),
	} {
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := listServiceFiles(
		serviceDirectory{path: first, suffix: ".service"},
		serviceDirectory{path: second, filePrefix: "S90"},
		serviceDirectory{path: filepath.Join(t.TempDir(), "missing")},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"api", "worker"}
	if !slices.Equal(got, want) {
		t.Fatalf("service names = %q, want %q", got, want)
	}
}

func TestFilterManagedServiceNames(t *testing.T) {
	got := filterManagedServiceNames([]string{"other", "lz_lz_worker", "lz_lz_api", "lz_lz_worker"})
	want := []string{"api", "worker"}
	if !slices.Equal(got, want) {
		t.Fatalf("service names = %q, want %q", got, want)
	}
}

func TestListServiceStatusesFiltersSortsAndMerges(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	for _, path := range []string{
		filepath.Join(first, "lz_lz_worker.service"),
		filepath.Join(first, "unmanaged.service"),
		filepath.Join(second, "S90lz_lz_api"),
		filepath.Join(second, "S90lz_lz_worker"),
	} {
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := listServiceStatuses(
		serviceDirectory{path: first, suffix: ".service", isRunning: func(string) bool { return false }},
		serviceDirectory{path: second, filePrefix: "S90", isRunning: func(name string) bool { return name == "lz_lz_worker" }},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []ServiceStatus{
		{Name: "api", Status: ServiceStopped},
		{Name: "worker", Status: ServiceRunning},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("service statuses = %#v, want %#v", got, want)
	}
}
