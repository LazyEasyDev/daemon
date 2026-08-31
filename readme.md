# DAEMON

Install applications as system services on Linux, macOS, FreeBSD, and Windows.

Run `./build.sh` to compile binaries into `build/`:

- `daemon-darwin-amd64`
- `daemon-darwin-arm64`
- `daemon-freebsd-amd64`
- `daemon-freebsd-arm64`
- `daemon-windows-amd64.exe`
- `daemon-windows-386.exe`
- `daemon-windows-arm64.exe`
- `daemon-linux-amd64`
- `daemon-linux-386`
- `daemon-linux-arm64`
- `daemon-linux-arm32`

## Architecture

`daemon-util` is a management CLI, not a shared long-running supervisor. It registers each application as an independent service with the platform's native service system, then exits. Lifecycle operations are delegated to systemd, OpenRC, OpenWrt procd, Upstart, System V or Buildroot init, FreeBSD rc.d, launchd, or Windows SCM. FreeBSD uses a separate `/usr/sbin/daemon` supervisor for each installed service. On Windows, ordinary applications use a separate `daemon-util` wrapper process for each service.

We considered using one shared background process to supervise every installed application, but rejected that design for the following reasons:

- The shared supervisor would be a single point of failure for every application it manages.
- Applications might survive a supervisor crash. When the supervisor restarts, safely identifying and adopting those processes is difficult; starting replacements could create duplicate application instances.
- Reimplementing process identity, restart policy, graceful shutdown, boot integration, dependencies, and permissions would duplicate behavior already owned by the operating system.
- A permanently running, privileged control process would add operational and security surface that this CLI does not otherwise need.
- Independent native services isolate failures and allow each application to be started, stopped, inspected, and recovered separately.

This choice intentionally favors native reliability and failure isolation over identical behavior on every platform. Restart and shutdown details therefore follow the capabilities and constraints of each service manager.

## Process containment

The generated service configuration uses each platform's native containment where it is available:

| Backend | Descendant handling on stop |
| --- | --- |
| Windows default mode | The per-service wrapper assigns the application to a Job Object and terminates the entire Job. |
| Windows native-service mode | The SCM-aware application is responsible for stopping its descendants. |
| systemd | `KillMode=control-group` stops processes remaining in the service cgroup. |
| OpenRC | `supervise-daemon --stop-group` stops the supervised process group. |
| System V | The application starts in a dedicated session and the script signals its process group. |
| macOS launchd | launchd's default process-group cleanup applies. |
| Buildroot, Upstart, OpenWrt, FreeBSD | The generated configuration adds no descendant guarantee beyond the service manager or supervisor's native behavior. |

Process-group containment on OpenRC, System V, and launchd cannot stop descendants that deliberately detach into another session or process group. Applications must remain in the foreground and must not daemonize or otherwise escape native supervision. On newer OpenWrt systems, procd may provide additional cgroup containment, but the generated stop path does not depend on it.


## How to use

1. Compile program according to the os-arch system. 
2. Copy the compiled file into your project package and rename it to `daemon` (`daemon.exe` on Windows).
3. Install the service using the privileges required by your platform:
	- macOS: run without `sudo`. The current implementation installs a per-user LaunchAgent.
	- Linux and FreeBSD: run with `sudo` because the service is installed system-wide.
	- Windows: use PowerShell or Command Prompt opened with **Run as administrator**. Windows Server does not require a `sudo` command.
4. Manage it with `start`, `stop`, `restart`, `status`, or `remove` using the same privilege mode.

### For example

```
├─{your-project-folder}
│  ├─configs    //config folder
│  ├─logs       //log folder
│  ├─assets     //assets folder
│  ├─myapp      //executable file
│  └─daemon    //daemon file compiled and copy from this package
```

The service name is explicit and independent from the executable filename. It must start with an ASCII letter, contain only ASCII letters and digits, and be at most 241 characters long. A relative executable name is resolved beside the daemon binary. An executable in another folder can be installed using its absolute path.

The application path must resolve to a native executable for the current operating system. Symbolic links are supported. On Linux and FreeBSD, run a script by installing its native interpreter as the application and passing the script path as the first argument, for example `sudo ./daemon install myservice /bin/sh /opt/myservice.sh`. On macOS, use the same command without `sudo`. The executable or interpreter must remain in the foreground for the service lifetime.

On Windows, ordinary third-party executables are supported by default and do not need to implement the Windows SCM protocol. Use `--windows-native-service` only when the target already implements that protocol. The CLI registers `myservice` internally as `lz_lz_myservice`, so an application using this package's `Daemon.Run` method must construct its daemon with the matching registration name:

```go
registrationName, err := daemon_util.ManagedServiceName("myservice")
if err != nil {
	return err
}
service, err := daemon_util.New(registrationName, "my service", daemon_util.SystemDaemon)
if err != nil {
	return err
}
_, err = service.Run(executable)
return err
```


### Linux and FreeBSD

```sh
cd ./{your-project-folder}

sudo ./daemon install myservice myapp [arg1] [arg2] ...
sudo ./daemon install --stop-timeout 45s myservice myapp [arg1] [arg2] ...
sudo ./daemon install myservice myapp --port 8080 --config "configs/my app.toml"
sudo ./daemon install myservice "/opt/My App/myapp" --port 8080 --config "/opt/My App/config.toml"
sudo ./daemon list
sudo ./daemon ls
sudo ./daemon start myservice
sudo ./daemon status myservice
sudo ./daemon restart myservice
sudo ./daemon stop myservice
sudo ./daemon remove myservice
```

### macOS

Do not use `sudo`; it would create the per-user LaunchAgent for the root user instead of the logged-in user.

```sh
cd ./{your-project-folder}

./daemon install myservice myapp [arg1] [arg2] ...
./daemon install --stop-timeout 45s myservice myapp [arg1] [arg2] ...
./daemon list
./daemon start myservice
./daemon status myservice
./daemon restart myservice
./daemon stop myservice
./daemon remove myservice
```

### Windows PowerShell

Open PowerShell with **Run as administrator**, then run:

```powershell
cd C:\path\to\your-project-folder

.\daemon.exe install myservice .\myapp.exe [arg1] [arg2] ...
.\daemon.exe install --stop-timeout 5m myservice .\myapp.exe [arg1] [arg2] ...
.\daemon.exe install --windows-native-service myservice .\scm-aware-app.exe [arg1] [arg2] ...
.\daemon.exe list
.\daemon.exe start myservice
.\daemon.exe status myservice
.\daemon.exe restart myservice
.\daemon.exe stop myservice
.\daemon.exe stop --stop-timeout 45s myservice
.\daemon.exe remove myservice
```

In the default mode, SCM runs `daemon.exe`, which starts the application in its executable directory and reports application startup or runtime failures to SCM. Nonzero application exits activate the configured SCM recovery actions. Stopping the service terminates the application's Job Object, including its descendants. Applications that need custom graceful shutdown handling should implement the SCM protocol and be installed with `--windows-native-service`.

Windows services use the LocalSystem account by default. Keep `daemon.exe`, the target executable, scripts, and their containing directories writable only by trusted administrators; otherwise an unprivileged user could replace code that SCM executes as LocalSystem.

`--stop-timeout` defaults to `600s` and accepts positive, whole-second Go duration values such as `45s` or `10m`. On Unix platforms, set it during `install` so the generated service configuration waits up to that duration before forcing termination. On Windows, the install value configures how long SCM allows the service to finish preshutdown cleanup during an operating-system shutdown or reboot. The option must appear before the executable because arguments after the executable belong to the application.

On Windows, `--stop-timeout` on `stop`, `restart`, or `remove` separately controls how long this tool waits for SCM to report `STOPPED`. A timeout from one of these commands returns an error and does not terminate the service process.

`list` and `ls` show each managed service, its current status, and the configured application path:

```text
NAME        STATUS   APP
api         stopped  /opt/api
myservice   running  /opt/myservice/current
```

The application path is informational and preserves the path supplied during installation, including a symbolic link. Metadata is stored in the platform application-data directory and does not control service operations. If metadata cannot be written or read, or is malformed, installation and listing still succeed and the `APP` column is blank for that service. Removing a service also removes its metadata on a best-effort basis.


### Test applications

Run `./test_app/build.sh` to compile test applications into `test_app/build/`.

#### Linux ARM64 example

```sh
sudo ./daemon install apptest test_app/build/test-app-linux-arm64
```

#### Linux ARM32 example (ARMv6, ARMv7, and compatible systems)

```sh
sudo ./daemon install apptest test_app/build/test-app-linux-arm32
```





