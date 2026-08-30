# daemon_util

`daemon_util` installs and controls applications as native operating-system services.

It supports:

- macOS through `launchd`
- FreeBSD through `rc.d`
- Linux through systemd, OpenRC, Upstart, OpenWrt/procd, Bobcat, or System V
- Windows through the Service Control Manager

[Package documentation](https://pkg.go.dev/github.com/LazyEasyDev/daemon/daemon_util)

## Installation

```sh
go get github.com/LazyEasyDev/daemon/daemon_util
```

```go
import daemon "github.com/LazyEasyDev/daemon/daemon_util"
```

## Creating a service

### Executable beside the current program

`New` resolves the managed executable from the directory containing the current program. This example looks for a file named `myapp` beside the running program:

```go
service, err := daemon.New(
    "myapp",
    "My application",
    daemon.SystemDaemon,
)
if err != nil {
    log.Fatal(err)
}
```

This also suits a self-installing application when its executable filename matches the service name.

### Executable at an absolute path

Use `NewWithExecutable` when the managed executable is elsewhere:

```go
service, err := daemon.NewWithExecutable(
    "myapp",
    "My application",
    "/opt/myapp/bin/myapp",
    daemon.SystemDaemon,
)
if err != nil {
    log.Fatal(err)
}
```

The executable path must be absolute. The service name remains independent from the path.

### Dependencies

Dependencies may be supplied after the daemon kind:

```go
service, err := daemon.NewWithExecutable(
    "myapp",
    "My application",
    "/opt/myapp/bin/myapp",
    daemon.SystemDaemon,
    "network-online.target",
)
```

Dependency handling is backend-specific. It is currently applied by systemd and Windows SCM.
On systemd, every dependency must be a literal unit name with a type suffix, such as
`network-online.target` or `worker@blue.service`.

## Daemon kinds

| Kind | Platforms | Scope |
| --- | --- | --- |
| `SystemDaemon` | Linux, FreeBSD, Windows | System-wide service |
| `UserAgent` | macOS | Per-user LaunchAgent |
| `GlobalAgent` | macOS | Administrator-provided global LaunchAgent |
| `GlobalDaemon` | macOS | System-wide LaunchDaemon |

An unsupported kind returns `ErrInvalidKind`.

## Managing a service

Every `Daemon` provides the same lifecycle methods:

```go
message, err := service.Install(
    "--port", "8080",
    "--config", "/etc/myapp/config.toml",
)
message, err = service.Start()
message, err = service.Status()
message, err = service.Stop()
message, err = service.Remove()
```

Always check the returned error:

```go
message, err := service.Start()
if err != nil {
    log.Fatalf("%s: %v", message, err)
}
fmt.Println(message)
```

Arguments passed to `Install` are stored in the native service definition and passed to the application whenever it starts. Arguments remain separate values, including spaces, empty strings, quotes, shell characters, and Windows paths.

Administrative privileges are generally required for system-wide lifecycle operations. A macOS `UserAgent` does not require root privileges.

## Managed service names

Command-line registrations use a reserved internal prefix so they can be discovered without maintaining a separate registry. Library users can opt into the same convention:

```go
name, err := daemon.ManagedServiceName("my-service")
if err != nil {
    log.Fatal(err)
}

service, err := daemon.NewWithExecutable(
    name,
    "My application",
    "/opt/myapp/bin/myapp",
    daemon.SystemDaemon,
)
```

`ListServices` returns only registrations using this convention and removes the internal prefix from every result:

```go
names, err := daemon.ListServices()
```

`ListServiceStatuses` returns the same managed registrations with their current platform-native status:

```go
services, err := daemon.ListServiceStatuses()
for _, service := range services {
    log.Printf("%s: %s", service.Name, service.Status)
}
```

Managed names must use only `A-Z`, `a-z`, `0-9`, `.`, `_`, `@`, and `-`; whitespace and the reserved prefix are rejected. Results are sorted and deduplicated. Windows registrations are enumerated from SCM; other platforms inspect their native service-definition directories.

## Running service code

`Run` accepts an implementation of `Executable`:

```go
type Executable interface {
    Start()
    Stop()
    Run()
}
```

On Windows, `Run` connects the process to the Service Control Manager when launched as a service and calls `Start` or `Stop` in response to service events. In an interactive session it calls `Run` directly. Unix implementations call `Run` directly.

```go
type App struct{}

func (App) Start() { go runServer() }
func (App) Stop()  { stopServer() }
func (App) Run()   { runServer() }

func main() {
    service, err := daemon.New("myapp", "My application", daemon.SystemDaemon)
    if err != nil {
        log.Fatal(err)
    }
    if _, err := service.Run(App{}); err != nil {
        log.Fatal(err)
    }
}
```

## Service names and metadata

Service names may contain only:

```text
A-Z a-z 0-9 . _ @ -
```

Whitespace runs are normalized to `_`. Empty names, `.` and `..` are rejected. Descriptions and dependency names must not contain NUL, carriage-return, or newline characters. FreeBSD derives shell-safe `RCName` and `RCVar` values when a valid service name cannot be used directly as a shell variable.

## Custom templates

Unix service definitions use Go `text/template` templates. Retrieve or replace the template on an individual daemon instance:

```go
current := service.GetTemplate()

if err := service.SetTemplate(customTemplate); err != nil {
    log.Fatal(err)
}
```

Custom templates are not supported on Windows. `GetTemplate` returns an empty string there, and `SetTemplate` returns an error.

Template data depends on the backend:

| Field | Type | Backends |
| --- | --- | --- |
| `Name` | `string` | All Unix backends |
| `Description` | `string` | FreeBSD and Linux |
| `Path` | `string` | All Unix backends |
| `Args` | `[]string` | macOS |
| `Args` | `string` | FreeBSD and Linux; already safely serialized |
| `Dependencies` | `string` | systemd |
| `RCName` | `string` | FreeBSD |
| `RCVar` | `string` | FreeBSD |
| `WorkingDirectory` | `string` | macOS |

Each backend exposes its relevant escaping helper to templates:

- `xml` on macOS
- `systemdQuote` and `systemdConfigQuote` on systemd
- `shellQuote` on FreeBSD and shell-based Linux backends

Templates are rendered to a temporary file and renamed atomically, preventing malformed or partially written service definitions from being installed.

## Errors

Use `errors.Is` with package sentinel errors:

```go
if errors.Is(err, daemon.ErrAlreadyRunning) {
    // The service is already running.
}
```

| Error | Meaning |
| --- | --- |
| `ErrInvalidName` | The service name is empty or contains unsupported characters |
| `ErrInvalidKind` | The daemon kind is unsupported on the current OS |
| `ErrInvalidExecutablePath` | `NewWithExecutable` received a non-absolute path |
| `ErrInvalidDependency` | A systemd dependency is not a literal unit name with a type suffix |
| `ErrUnsupportedSystem` | No supported service backend was detected |
| `ErrRootPrivileges` | The operation requires elevated privileges |
| `ErrAlreadyInstalled` | The service definition already exists |
| `ErrNotInstalled` | The service is not installed |
| `ErrAlreadyRunning` | The service is already running |
| `ErrAlreadyStopped` | The service is already stopped |

## Platform notes

- Linux chooses its backend at runtime, preferring systemd, OpenRC, and Upstart before distribution-specific and System V backends.
- OpenRC services are added to the `default` runlevel and removed from all runlevels during uninstall.
- FreeBSD asks `service <name> enabled` and may use `one<command>` when the service is not enabled.
- Windows service definitions are managed by SCM rather than text templates.
- Status and lifecycle behavior ultimately depend on the native service manager.

## License

[MIT License](../LICENSE)