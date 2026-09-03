package main

import (
	"context"
	"log"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/LazyEasyDev/daemon/daemon_util"
)

func stopTimeoutFlag() cli.Flag {
	return &cli.DurationFlag{
		Name:  "stop-timeout",
		Usage: "maximum graceful stop duration before forced termination where supported",
		Value: daemon_util.DefaultStopTimeout,
	}
}

func ignoreWarningsFlag() cli.Flag {
	return &cli.BoolFlag{
		Name:  "ignore-warnings",
		Usage: "skip installation warnings and confirmation prompts",
	}
}

func newCommand() *cli.Command {
	stopAfterInstallTarget := 2
	commands := []*cli.Command{
		{
			Name:         "install",
			Usage:        "install app as system service",
			ArgsUsage:    "<service-name> <app-or-absolute-path> [app arguments...]",
			StopOnNthArg: &stopAfterInstallTarget,
			Flags:        []cli.Flag{stopTimeoutFlag(), ignoreWarningsFlag()},
			Action:       install,
		},
		{
			Name:    "list",
			Aliases: []string{"ls"},
			Usage:   "list services installed by this tool",
			Flags: []cli.Flag{&cli.BoolFlag{
				Name:    "long",
				Aliases: []string{"l"},
				Usage:   "show application arguments",
			}},
			Action: list,
		},
		{
			Name:    "remove",
			Aliases: []string{"delete"},
			Usage:   "remove app from system service",
			Action:  remove,
		},
		{
			Name:   "start",
			Usage:  "start app",
			Action: start,
		},
		{
			Name:   "stop",
			Usage:  "stop app",
			Action: stop,
		},
		{
			Name:   "restart",
			Usage:  "restart app",
			Action: restart,
		},
		{
			Name:   "status",
			Usage:  "show app status",
			Action: status,
		},
	}
	commands = append(commands, platformCommands()...)

	return &cli.Command{
		Name:     "daemon-util",
		Usage:    "run app as system service",
		Commands: commands,
	}
}

func main() {
	app := newCommand()
	if err := app.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
