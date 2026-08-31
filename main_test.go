package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/LazyEasyDev/daemon/daemon_util"
)

type stopStub struct {
	err error
}

func (stub stopStub) Stop() (string, error) {
	return "", stub.err
}

func TestInstallCommandPreservesApplicationArguments(t *testing.T) {
	app := newCommand()
	installCommand := app.Command("install")
	if installCommand == nil {
		t.Fatal("install command not found")
	}

	var got []string
	installCommand.Action = func(_ context.Context, command *cli.Command) error {
		got = command.Args().Slice()
		return nil
	}

	want := []string{"worker", "myapp", "--port", "8080", "value with spaces", ""}
	args := append([]string{"daemon-util", "install"}, want...)
	if err := app.Run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("install arguments = %q, want %q", got, want)
	}
}

func TestInstallCommandPreservesArgumentsAfterAbsolutePath(t *testing.T) {
	app := newCommand()
	installCommand := app.Command("install")
	if installCommand == nil {
		t.Fatal("install command not found")
	}

	var got []string
	installCommand.Action = func(_ context.Context, command *cli.Command) error {
		got = command.Args().Slice()
		return nil
	}

	appPath := filepath.Join(string(filepath.Separator), "opt", "My App", "myapp")
	want := []string{"worker", appPath, "--port", "8080", "value with spaces", ""}
	args := append([]string{"daemon-util", "install"}, want...)
	if err := app.Run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("install arguments = %q, want %q", got, want)
	}
}

func TestInstallCommandParsesStopTimeoutBeforeApplication(t *testing.T) {
	app := newCommand()
	installCommand := app.Command("install")
	if installCommand == nil {
		t.Fatal("install command not found")
	}

	var gotTimeout time.Duration
	var gotArgs []string
	installCommand.Action = func(_ context.Context, command *cli.Command) error {
		gotTimeout = command.Duration("stop-timeout")
		gotArgs = command.Args().Slice()
		return nil
	}

	args := []string{"daemon-util", "install", "--stop-timeout", "45s", "worker", "myapp", "--port", "8080"}
	if err := app.Run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if gotTimeout != 45*time.Second {
		t.Fatalf("stop timeout = %v, want 45s", gotTimeout)
	}
	wantArgs := []string{"worker", "myapp", "--port", "8080"}
	if !slices.Equal(gotArgs, wantArgs) {
		t.Fatalf("install arguments = %q, want %q", gotArgs, wantArgs)
	}
}

func TestStopTimeoutDefaultsToTenMinutes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the stop command timeout is specific to Windows")
	}
	app := newCommand()
	stopCommand := app.Command("stop")
	if stopCommand == nil {
		t.Fatal("stop command not found")
	}

	var got time.Duration
	stopCommand.Action = func(_ context.Context, command *cli.Command) error {
		got = command.Duration("stop-timeout")
		return nil
	}
	if err := app.Run(context.Background(), []string{"daemon-util", "stop", "worker"}); err != nil {
		t.Fatal(err)
	}
	if got != daemon_util.DefaultStopTimeout {
		t.Fatalf("stop timeout = %v, want %v", got, daemon_util.DefaultStopTimeout)
	}
}

func TestInstallCommandRequiresServiceNameAndExecutable(t *testing.T) {
	app := newCommand()
	err := app.Run(context.Background(), []string{"daemon-util", "install", "myapp"})
	if err == nil {
		t.Fatal("install accepted an executable without an explicit service name")
	}
}

func TestListCommandHasLSAlias(t *testing.T) {
	app := newCommand()
	listCommand := app.Command("list")
	if listCommand == nil {
		t.Fatal("list command not found")
	}

	called := false
	listCommand.Action = func(context.Context, *cli.Command) error {
		called = true
		return nil
	}
	if err := app.Run(context.Background(), []string{"daemon-util", "ls"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("ls did not invoke the list command")
	}
}

func TestWriteServiceList(t *testing.T) {
	var output bytes.Buffer
	services := []daemon_util.ServiceStatus{
		{Name: "api", Status: daemon_util.ServiceStopped},
		{Name: "worker", Status: daemon_util.ServiceRunning, ApplicationPath: "/opt/worker"},
	}
	if err := writeServiceList(&output, services); err != nil {
		t.Fatal(err)
	}

	want := "NAME    STATUS   APP\napi     stopped  \nworker  running  /opt/worker\n"
	if output.String() != want {
		t.Fatalf("list output = %q, want %q", output.String(), want)
	}
}

func TestStopIfRunning(t *testing.T) {
	stopFailure := errors.New("stop failed")
	tests := []struct {
		name    string
		err     error
		wantErr error
	}{
		{name: "running service stops", err: nil},
		{name: "already stopped is accepted", err: daemon_util.ErrAlreadyStopped},
		{name: "stop failure is returned", err: stopFailure, wantErr: stopFailure},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := stopIfRunning(stopStub{err: test.err})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("stopIfRunning() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}
