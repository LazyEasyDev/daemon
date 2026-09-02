package daemon_util

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var (
	ErrInvalidName           = errors.New("invalid service name")
	ErrInvalidKind           = errors.New("invalid daemon kind")
	ErrInvalidExecutablePath = errors.New("invalid executable path")
	ErrInvalidStopTimeout    = errors.New("invalid stop timeout")
)

const DefaultStopTimeout = 600 * time.Second

type serviceConfig struct {
	stopTimeout time.Duration
}

func (config *serviceConfig) SetStopTimeout(timeout time.Duration) error {
	if timeout <= 0 || timeout%time.Second != 0 {
		return fmt.Errorf("%w: must be a positive whole number of seconds", ErrInvalidStopTimeout)
	}
	config.stopTimeout = timeout
	return nil
}

func (config *serviceConfig) stopTimeoutDuration() time.Duration {
	if config.stopTimeout == 0 {
		return DefaultStopTimeout
	}
	return config.stopTimeout
}

func (config *serviceConfig) stopTimeoutSeconds() int64 {
	return int64(config.stopTimeoutDuration() / time.Second)
}

const (
	managedServicePrefix      = "lz_lz_"
	maxRegistrationNameLength = 247
	maxServiceNameLength      = maxRegistrationNameLength - len(managedServicePrefix)
)

const (
	ServiceRunning = "running"
	ServiceStopped = "stopped"
)

// ServiceStatus describes a service registered by this tool.
type ServiceStatus struct {
	Name            string
	Status          string
	ApplicationPath string
	Arguments       string
}

// ManagedServiceName returns the internal registration name for a user-facing
// service name.
func ManagedServiceName(name string) (string, error) {
	if strings.HasPrefix(name, managedServicePrefix) {
		return "", fmt.Errorf("%w: prefix %q is reserved", ErrInvalidName, managedServicePrefix)
	}
	if err := validateUserServiceName(name); err != nil {
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
}

type stopTimeoutConfigurer interface {
	SetStopTimeout(time.Duration) error
}

// ConfigureStopTimeout configures the maximum graceful stop duration when the
// selected platform backend supports it.
func ConfigureStopTimeout(daemon Daemon, timeout time.Duration) error {
	configurer, ok := daemon.(stopTimeoutConfigurer)
	if !ok {
		return ErrInvalidStopTimeout
	}
	return configurer.SetStopTimeout(timeout)
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
func New(name, description string, kind Kind) (Daemon, error) {
	return newDaemonWithExecutable(name, description, kind, "")
}

// NewWithExecutable creates a daemon for an executable at an absolute path.
func NewWithExecutable(name, description, executablePath string, kind Kind) (Daemon, error) {
	if !filepath.IsAbs(executablePath) {
		return nil, fmt.Errorf("%w: path must be absolute", ErrInvalidExecutablePath)
	}
	return newDaemonWithExecutable(name, description, kind, filepath.Clean(executablePath))
}

func newDaemonWithExecutable(name, description string, kind Kind, executablePath string) (Daemon, error) {
	if err := validateServiceName(name); err != nil {
		return nil, err
	}
	if strings.ContainsAny(description, "\x00\r\n") {
		return nil, errors.New("description must be a single line")
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

	return newDaemon(name, description, kind, executablePath)
}

func validateServiceName(name string) error {
	if name == "" {
		return ErrInvalidName
	}
	if len(name) > maxRegistrationNameLength {
		return fmt.Errorf("%w: must not exceed %d characters", ErrInvalidName, maxRegistrationNameLength)
	}
	if !isASCIILetter(rune(name[0])) {
		return fmt.Errorf("%w: must start with an ASCII letter", ErrInvalidName)
	}

	for _, character := range name[1:] {
		if isASCIILetter(character) || character >= '0' && character <= '9' || character == '_' {
			continue
		}
		return fmt.Errorf("%w: unsupported character %q", ErrInvalidName, character)
	}

	return nil
}

func validateUserServiceName(name string) error {
	if name == "" {
		return ErrInvalidName
	}
	if len(name) > maxServiceNameLength {
		return fmt.Errorf("%w: must not exceed %d characters", ErrInvalidName, maxServiceNameLength)
	}
	if !isASCIILetter(rune(name[0])) {
		return fmt.Errorf("%w: must start with an ASCII letter", ErrInvalidName)
	}

	for _, character := range name[1:] {
		if isASCIILetter(character) || character >= '0' && character <= '9' {
			continue
		}
		return fmt.Errorf("%w: unsupported character %q", ErrInvalidName, character)
	}

	return nil
}

func isASCIILetter(character rune) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
}
