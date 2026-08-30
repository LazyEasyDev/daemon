package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

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
		{Name: "worker", Status: daemon_util.ServiceRunning},
	}
	if err := writeServiceList(&output, services); err != nil {
		t.Fatal(err)
	}

	want := "NAME    STATUS\napi     stopped\nworker  running\n"
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
