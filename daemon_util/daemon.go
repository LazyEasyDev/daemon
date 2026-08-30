package daemon_util

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	ErrInvalidName           = errors.New("invalid service name")
	ErrInvalidKind           = errors.New("invalid daemon kind")
	ErrInvalidExecutablePath = errors.New("invalid executable path")
)

const managedServicePrefix = "lz_lz_"

const (
	ServiceRunning = "running"
	ServiceStopped = "stopped"
)

// ServiceStatus describes a service registered by this tool.
type ServiceStatus struct {
	Name   string
	Status string
}

// ManagedServiceName returns the internal registration name for a user-facing
// service name.
func ManagedServiceName(name string) (string, error) {
	if strings.HasPrefix(name, managedServicePrefix) {
		return "", fmt.Errorf("%w: prefix %q is reserved", ErrInvalidName, managedServicePrefix)
	}
	if err := validateServiceName(name); err != nil {
		return "", err
	}
	return managedServicePrefix + name, nil
}

func logicalServiceName(name string) (string, bool) {
	if !strings.HasPrefix(name, managedServicePrefix) {
		return "", false
	}
	name = strings.TrimPrefix(name, managedServicePrefix)
	return name, name != ""
}

// Daemon interface has a standard set of methods/commands
type Daemon interface {
	// GetTemplate - gets service config template
	GetTemplate() string

	// SetTemplate - sets service config template
	SetTemplate(string) error

	// Install the service into the system
	Install(args ...string) (string, error)

	// Remove the service and all corresponding files from the system
	Remove() (string, error)

	// Start the service
	Start() (string, error)

	// Stop the service
	Stop() (string, error)

	// Status - check the service status
	Status() (string, error)

	// Run - run executable service
	Run(e Executable) (string, error)
}

// Executable interface defines controlling methods of executable service
type Executable interface {
	// Start starts the service and returns after startup has completed.
	Start()
	// Stop stops the service and returns after cleanup has completed.
	Stop()
	// Run runs the service until it is stopped.
	Run()
}

// Kind is type of the daemon
type Kind string

const (
	// UserAgent is a user daemon that runs as the currently logged in user and
	// stores its property list in the user’s individual LaunchAgents directory.
	// In other words, per-user agents provided by the user. Valid for macOS only.
	UserAgent Kind = "UserAgent"

	// GlobalAgent is a user daemon that runs as the currently logged in user and
	// stores its property list in the users' global LaunchAgents directory. In
	// other words, per-user agents provided by the administrator. Valid for macOS
	// only.
	GlobalAgent Kind = "GlobalAgent"

	// GlobalDaemon is a system daemon that runs as the root user and stores its
	// property list in the global LaunchDaemons directory. In other words,
	// system-wide daemons provided by the administrator. Valid for macOS only.
	GlobalDaemon Kind = "GlobalDaemon"

	// SystemDaemon is a system daemon that runs as the root user. In other words,
	// system-wide daemons provided by the administrator. Valid for FreeBSD, Linux
	// and Windows only.
	SystemDaemon Kind = "SystemDaemon"
)

// New - Create a new daemon
//
// name: name of the service
//
// description: any explanation, what is the service, its purpose
//
// kind: what kind of daemon to create
func New(name, description string, kind Kind, dependencies ...string) (Daemon, error) {
	return newDaemonWithExecutable(name, description, kind, "", dependencies)
}

// NewWithExecutable creates a daemon for an executable at an absolute path.
func NewWithExecutable(name, description, executablePath string, kind Kind, dependencies ...string) (Daemon, error) {
	if !filepath.IsAbs(executablePath) {
		return nil, fmt.Errorf("%w: path must be absolute", ErrInvalidExecutablePath)
	}
	return newDaemonWithExecutable(name, description, kind, filepath.Clean(executablePath), dependencies)
}

func newDaemonWithExecutable(name, description string, kind Kind, executablePath string, dependencies []string) (Daemon, error) {
	name = strings.Join(strings.Fields(name), "_")
	if err := validateServiceName(name); err != nil {
		return nil, err
	}
	if strings.ContainsAny(description, "\x00\r\n") {
		return nil, errors.New("description must be a single line")
	}
	for _, dependency := range dependencies {
		if strings.ContainsAny(dependency, "\x00\r\n") {
			return nil, fmt.Errorf("dependency %q must be a single line", dependency)
		}
	}

	switch runtime.GOOS {
	case "darwin":
		if kind != UserAgent && kind != GlobalAgent && kind != GlobalDaemon {
			return nil, fmt.Errorf("%w: %q", ErrInvalidKind, kind)
		}
	case "freebsd":
		if kind != SystemDaemon {
			return nil, fmt.Errorf("%w: %q", ErrInvalidKind, kind)
		}
	case "linux":
		if kind != SystemDaemon {
			return nil, fmt.Errorf("%w: %q", ErrInvalidKind, kind)
		}
	case "windows":
		if kind != SystemDaemon {
			return nil, fmt.Errorf("%w: %q", ErrInvalidKind, kind)
		}
	}

	return newDaemon(name, description, kind, dependencies, executablePath)
}

func validateServiceName(name string) error {
	if name == "" || name == "." || name == ".." {
		return ErrInvalidName
	}

	for _, character := range name {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("._@-", character) {
			continue
		}
		return fmt.Errorf("%w: unsupported character %q", ErrInvalidName, character)
	}

	return nil
}
