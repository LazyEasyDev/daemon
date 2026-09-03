package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/LazyEasyDev/daemon/daemon_util"
)

func newService(serviceName, executablePath string) (daemon_util.Daemon, error) {
	registrationName, err := daemon_util.ManagedServiceName(serviceName)
	if err != nil {
		return nil, err
	}
	kind := daemon_util.SystemDaemon
	if runtime.GOOS == "darwin" {
		kind = daemon_util.UserAgent
	}
	if executablePath != "" {
		return daemon_util.NewWithExecutable(registrationName, serviceName, executablePath, kind)
	}
	return daemon_util.New(registrationName, serviceName, kind)
}

func configuredService(command *cli.Command, serviceName, executablePath string) (daemon_util.Daemon, error) {
	service, err := newService(serviceName, executablePath)
	if err != nil {
		return nil, err
	}
	timeout := command.Duration("stop-timeout")
	if err := daemon_util.ConfigureStopTimeout(service, timeout); err != nil {
		return nil, err
	}
	return service, nil
}

func executablePathFromTarget(target string) (string, error) {
	if target == "" {
		return "", errors.New("executable path error")
	}
	path := target
	if !filepath.IsAbs(path) {
		executable, err := daemon_util.ExecPath()
		if err != nil {
			return "", err
		}
		path = filepath.Join(filepath.Dir(executable), path)
	}
	return daemon_util.ResolveExecutablePath(path)
}

func confirmInstallWarning(input io.Reader, output io.Writer, warning string, ignoreWarnings, interactive bool) error {
	if ignoreWarnings {
		return nil
	}
	if _, err := fmt.Fprintln(output, warning); err != nil {
		return nil
	}
	if !interactive {
		return errors.New("installation stopped because warning confirmation requires a terminal; rerun with --ignore-warnings to continue")
	}
	if _, err := fmt.Fprint(output, "Continue installation? [y/N] "); err != nil {
		return nil
	}

	scanner := bufio.NewScanner(input)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil
		}
		return errors.New("installation cancelled")
	}
	response := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if response != "y" && response != "yes" {
		return errors.New("installation cancelled")
	}
	return nil
}

func install(_ context.Context, command *cli.Command) error {
	args := command.Args().Slice()
	if len(args) < 2 {
		return errors.New("install requires a service name and executable")
	}
	executablePath, err := executablePathFromTarget(args[1])
	if err != nil {
		return err
	}
	service, serviceArgs, err := configuredInstallService(command, args[0], executablePath)
	if err != nil {
		return err
	}

	serviceArgs = append(serviceArgs, args[2:]...)
	result, err := service.Install(serviceArgs...)
	if err != nil {
		return err
	}
	if err := writeServiceMetadata(args[0], executablePath, strings.Join(args[2:], " "), command.Duration("stop-timeout")); err != nil {
		_ = removeServiceMetadata(args[0])
	}
	fmt.Println(result)

	return nil
}

func list(_ context.Context, command *cli.Command) error {
	services, err := daemon_util.ListServiceStatuses()
	if err != nil {
		return err
	}
	for index := range services {
		metadata := readServiceMetadataRecord(services[index].Name)
		services[index].ApplicationPath = metadata.ApplicationPath
		services[index].Arguments = metadata.Arguments
	}
	return writeServiceList(os.Stdout, services, command.Bool("long"))
}

func writeServiceList(output io.Writer, services []daemon_util.ServiceStatus, long bool) error {
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	header := "NAME\tSTATUS\tAPP"
	if long {
		header += "\tARGS"
	}
	if _, err := fmt.Fprintln(writer, header); err != nil {
		return err
	}
	for _, service := range services {
		line := fmt.Sprintf("%s\t%s\t%s", service.Name, service.Status, service.ApplicationPath)
		if long {
			line += "\t" + service.Arguments
		}
		if _, err := fmt.Fprintln(writer, line); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func requiredServiceName(command *cli.Command) (string, error) {
	args := command.Args().Slice()
	if len(args) != 1 {
		return "", fmt.Errorf("%s requires exactly one service name", command.Name)
	}
	return args[0], nil
}

func remove(_ context.Context, command *cli.Command) error {
	serviceName, err := requiredServiceName(command)
	if err != nil {
		return err
	}
	service, err := newService(serviceName, "")
	if err != nil {
		return err
	}

	if err := stopIfRunningWithProgress(serviceName, service); err != nil {
		return err
	}
	result, err := service.Remove()
	if err != nil {
		return err
	}
	_ = removeServiceMetadata(serviceName)
	fmt.Println(result)

	return nil
}

func start(_ context.Context, command *cli.Command) error {
	serviceName, err := requiredServiceName(command)
	if err != nil {
		return err
	}
	service, err := newService(serviceName, "")
	if err != nil {
		return err
	}
	result, err := service.Start()
	if err != nil {
		return err
	}
	fmt.Println(result)

	return nil
}

func stop(_ context.Context, command *cli.Command) error {
	serviceName, err := requiredServiceName(command)
	if err != nil {
		return err
	}
	service, err := newService(serviceName, "")
	if err != nil {
		return err
	}
	result, err := stopWithProgress(serviceName, service)
	if err != nil {
		return err
	}
	fmt.Println(result)

	return nil
}

func restart(_ context.Context, command *cli.Command) error {
	serviceName, err := requiredServiceName(command)
	if err != nil {
		return err
	}
	service, err := newService(serviceName, "")
	if err != nil {
		return err
	}
	if err := stopIfRunningWithProgress(serviceName, service); err != nil {
		return err
	}
	result, err := service.Start()
	if err != nil {
		return err
	}
	fmt.Println(result)

	return nil
}

type stoppable interface {
	Stop() (string, error)
}

func stopWithProgress(serviceName string, service stoppable) (string, error) {
	finishProgress := beginServiceStopProgress(serviceName)
	result, err := service.Stop()
	finishProgress()
	return result, err
}

func stopIfRunningWithProgress(serviceName string, service stoppable) error {
	_, err := stopWithProgress(serviceName, service)
	if errors.Is(err, daemon_util.ErrAlreadyStopped) {
		return nil
	}
	return err
}

func beginServiceStopProgress(serviceName string) func() {
	if !isTerminal(os.Stderr) {
		return func() {}
	}
	approximateWait, _ := readServiceStopTimeout(serviceName)
	return beginStopProgress(os.Stderr, serviceName, approximateWait, time.Second)
}

func beginStopProgress(writer io.Writer, serviceName string, approximateWait, interval time.Duration) func() {
	done := make(chan struct{})
	stopped := make(chan struct{})
	startedAt := time.Now()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		defer close(stopped)

		lineWidth := 0
		for {
			select {
			case now := <-ticker.C:
				message := formatStopProgress(serviceName, now.Sub(startedAt), approximateWait)
				padding := max(lineWidth-len(message), 0)
				_, _ = fmt.Fprint(writer, "\r", message, strings.Repeat(" ", padding))
				lineWidth = len(message)
			case <-done:
				if lineWidth > 0 {
					_, _ = fmt.Fprint(writer, "\r", strings.Repeat(" ", lineWidth), "\r")
				}
				return
			}
		}
	}()

	return func() {
		close(done)
		<-stopped
	}
}

func formatStopProgress(serviceName string, elapsed, approximateWait time.Duration) string {
	seconds := max(int64(elapsed/time.Second), 1)
	if approximateWait > 0 {
		return fmt.Sprintf("Stopping %s... %ds elapsed (within time limit: %s)", serviceName, seconds, approximateWait)
	}
	return fmt.Sprintf("Stopping %s... %ds elapsed", serviceName, seconds)
}

func status(_ context.Context, command *cli.Command) error {
	serviceName, err := requiredServiceName(command)
	if err != nil {
		return err
	}
	service, err := newService(serviceName, "")
	if err != nil {
		return err
	}
	result, err := service.Status()
	if err != nil {
		return err
	}
	fmt.Println(result)

	return nil
}
