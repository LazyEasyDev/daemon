//go:build !windows

package main

import "github.com/urfave/cli/v3"

func installCommandFlags() []cli.Flag {
	return []cli.Flag{stopTimeoutFlag()}
}

func stopCommandFlags() []cli.Flag {
	return nil
}
