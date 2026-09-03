package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/LazyEasyDev/daemon/daemon_util"
	"github.com/urfave/cli/v3"
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

func TestInstallCommandParsesIgnoreWarnings(t *testing.T) {
	app := newCommand()
	installCommand := app.Command("install")
	if installCommand == nil {
		t.Fatal("install command not found")
	}

	var ignoreWarnings bool
	installCommand.Action = func(_ context.Context, command *cli.Command) error {
		ignoreWarnings = command.Bool("ignore-warnings")
		return nil
	}

	args := []string{"daemon-util", "install", "--ignore-warnings", "worker", "myapp"}
	if err := app.Run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if !ignoreWarnings {
		t.Fatal("--ignore-warnings was not enabled")
	}
}

func TestInstallCommandRejectsZeroStopTimeout(t *testing.T) {
	app := newCommand()
	installCommand := app.Command("install")
	if installCommand == nil {
		t.Fatal("install command not found")
	}
	installCommand.Action = func(_ context.Context, command *cli.Command) error {
		_, err := configuredService(command, "worker", "")
		return err
	}

	err := app.Run(context.Background(), []string{"daemon-util", "install", "--stop-timeout", "0s", "worker", "myapp"})
	if !errors.Is(err, daemon_util.ErrInvalidStopTimeout) {
		t.Fatalf("zero stop timeout error = %v, want %v", err, daemon_util.ErrInvalidStopTimeout)
	}
}

func TestStopTimeoutIsInstallOnly(t *testing.T) {
	app := newCommand()
	for _, commandName := range []string{"stop", "restart", "remove"} {
		command := app.Command(commandName)
		if command == nil {
			t.Fatalf("%s command not found", commandName)
		}
		if len(command.Flags) != 0 {
			t.Errorf("%s flags = %v, want none", commandName, command.Flags)
		}
	}
}

func TestInstallCommandRequiresServiceNameAndExecutable(t *testing.T) {
	app := newCommand()
	err := app.Run(context.Background(), []string{"daemon-util", "install", "myapp"})
	if err == nil {
		t.Fatal("install accepted an executable without an explicit service name")
	}
}

func TestLifecycleCommandsRequireExactlyOneServiceName(t *testing.T) {
	for _, commandName := range []string{"remove", "start", "stop", "restart", "status"} {
		for _, args := range [][]string{nil, {"worker", "extra"}} {
			t.Run(fmt.Sprintf("%s/%d arguments", commandName, len(args)), func(t *testing.T) {
				appArgs := append([]string{"daemon-util", commandName}, args...)
				err := newCommand().Run(context.Background(), appArgs)
				want := commandName + " requires exactly one service name"
				if err == nil || err.Error() != want {
					t.Fatalf("error = %v, want %q", err, want)
				}
			})
		}
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

func TestListCommandAcceptsLongFlag(t *testing.T) {
	app := newCommand()
	listCommand := app.Command("list")
	if listCommand == nil {
		t.Fatal("list command not found")
	}

	var long bool
	listCommand.Action = func(_ context.Context, command *cli.Command) error {
		long = command.Bool("long")
		return nil
	}
	if err := app.Run(context.Background(), []string{"daemon-util", "ls", "-l"}); err != nil {
		t.Fatal(err)
	}
	if !long {
		t.Fatal("ls -l did not enable long output")
	}
}

func TestRemoveCommandHasDeleteAlias(t *testing.T) {
	app := newCommand()
	removeCommand := app.Command("remove")
	if removeCommand == nil {
		t.Fatal("remove command not found")
	}

	called := false
	removeCommand.Action = func(context.Context, *cli.Command) error {
		called = true
		return nil
	}
	if err := app.Run(context.Background(), []string{"daemon-util", "delete"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("delete did not invoke the remove command")
	}
}

func TestWriteServiceList(t *testing.T) {
	var output bytes.Buffer
	services := []daemon_util.ServiceStatus{
		{Name: "api", Status: daemon_util.ServiceStopped},
		{Name: "worker", Status: daemon_util.ServiceRunning, ApplicationPath: "/opt/worker", Arguments: "--message hello world"},
	}
	if err := writeServiceList(&output, services, false); err != nil {
		t.Fatal(err)
	}

	want := "NAME    STATUS   APP\napi     stopped  \nworker  running  /opt/worker\n"
	if output.String() != want {
		t.Fatalf("list output = %q, want %q", output.String(), want)
	}
}

func TestWriteLongServiceList(t *testing.T) {
	var output bytes.Buffer
	services := []daemon_util.ServiceStatus{
		{Name: "api", Status: daemon_util.ServiceStopped},
		{Name: "worker", Status: daemon_util.ServiceRunning, ApplicationPath: "/opt/worker", Arguments: "--message hello world"},
	}
	if err := writeServiceList(&output, services, true); err != nil {
		t.Fatal(err)
	}

	want := "NAME    STATUS   APP          ARGS\napi     stopped               \nworker  running  /opt/worker  --message hello world\n"
	if output.String() != want {
		t.Fatalf("long list output = %q, want %q", output.String(), want)
	}
}

func TestStopIfRunningWithProgress(t *testing.T) {
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
			err := stopIfRunningWithProgress("worker", stopStub{err: test.err})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("stopIfRunningWithProgress() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}
