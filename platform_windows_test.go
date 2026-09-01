//go:build windows

package main

import (
	"context"
	"slices"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestWindowsServiceCommandPreservesApplicationArguments(t *testing.T) {
	app := newCommand()
	serviceCommand := app.Command(windowsServiceCommand)
	if serviceCommand == nil {
		t.Fatal("Windows service command not found")
	}

	var got []string
	serviceCommand.Action = func(_ context.Context, command *cli.Command) error {
		got = command.Args().Slice()
		return nil
	}
	want := []string{"worker", `C:\My App\worker.exe`, "--port", "8080", "value with spaces", ""}
	args := append([]string{"daemon-util", windowsServiceCommand}, want...)
	if err := app.Run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("service arguments = %q, want %q", got, want)
	}
}
