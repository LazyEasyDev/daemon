package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"text/tabwriter"

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
	_ = writeServiceMetadata(args[0], executablePath)
	fmt.Println(result)

	return nil
}

func list(_ context.Context, _ *cli.Command) error {
	services, err := daemon_util.ListServiceStatuses()
	if err != nil {
		return err
	}
	for index := range services {
		services[index].ApplicationPath = readServiceMetadata(services[index].Name)
	}
	return writeServiceList(os.Stdout, services)
}

func writeServiceList(output io.Writer, services []daemon_util.ServiceStatus) error {
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "NAME\tSTATUS\tAPP"); err != nil {
		return err
	}
	for _, service := range services {
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\n", service.Name, service.Status, service.ApplicationPath); err != nil {
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

	if err := stopIfRunning(service); err != nil {
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
	result, err := service.Stop()
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
	if err := stopIfRunning(service); err != nil {
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

func stopIfRunning(service stoppable) error {
	_, err := service.Stop()
	if errors.Is(err, daemon_util.ErrAlreadyStopped) {
		return nil
	}
	return err
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
