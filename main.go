package main

import (
	"context"
	"log"
	"os"

	"github.com/urfave/cli/v3"
)

func newCommand() *cli.Command {
	stopAfterInstallTarget := 2

	return &cli.Command{
		Name:  "daemon-util",
		Usage: "run app as system service",
		Commands: []*cli.Command{
			{
				Name:         "install",
				Usage:        "install app as system service",
				ArgsUsage:    "<service-name> <app-or-absolute-path> [app arguments...]",
				StopOnNthArg: &stopAfterInstallTarget,
				Action:       install,
			},
			{
				Name:    "list",
				Aliases: []string{"ls"},
				Usage:   "list services installed by this tool",
				Action:  list,
			},
			{
				Name:   "remove",
				Usage:  "remove app from system service",
				Action: remove,
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
		},
	}
}

func main() {
	app := newCommand()
	if err := app.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
