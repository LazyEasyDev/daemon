# DAEMON

Install app as system service [support Linux, Darwin, FreeBSD and Windows]

Precompiled files in /build

daemon-darwin-amd64<br />
daemon-darwin-arm64<br />
daemon-freebsd-amd64<br />
daemon-freebsd-arm64<br />
daemon-windows-amd64<br />
daemon-windows-386<br />
daemon-windows-arm64<br />
daemon-linux-amd64<br />
daemon-linux-386<br />
daemon-linux-arm64<br />
daemon-linux-arm32<br />

## Architecture

`daemon-util` is a management CLI, not a long-running supervisor. It registers each application as an independent service with the platform's native service system, then exits. Lifecycle operations are delegated to systemd, OpenRC, OpenWrt procd, Upstart, System V or Buildroot init, FreeBSD rc.d, launchd, or Windows SCM. FreeBSD uses a separate `/usr/sbin/daemon` supervisor for each installed service.

We considered using one shared background process to supervise every installed application, but rejected that design for the following reasons:

- The shared supervisor would be a single point of failure for every application it manages.
- Applications might survive a supervisor crash. When the supervisor restarts, safely identifying and adopting those processes is difficult; starting replacements could create duplicate application instances.
- Reimplementing process identity, restart policy, graceful shutdown, boot integration, dependencies, and permissions would duplicate behavior already owned by the operating system.
- A permanently running, privileged control process would add operational and security surface that this CLI does not otherwise need.
- Independent native services isolate failures and allow each application to be started, stopped, inspected, and recovered separately.

This choice intentionally favors native reliability and failure isolation over identical behavior on every platform. Restart and shutdown details therefore follow the capabilities and constraints of each service manager.


## How to use
1. Compile program according to the os-arch system. 
2. Copy the compiled file into your project package and rename it to `daemon` (`daemon.exe` on Windows).
3. Install the service using the privileges required by your platform:
	- macOS: run without `sudo`. The current implementation installs a per-user LaunchAgent.
	- Linux and FreeBSD: run with `sudo` because the service is installed system-wide.
	- Windows: use PowerShell or Command Prompt opened with **Run as administrator**. Windows Server does not require a `sudo` command.
4. Manage it with `start`, `stop`, `restart`, `status`, or `remove` using the same privilege mode.

### for example
```
├─{your-project-folder}
│  ├─configs    //cofig folder
│  ├─logs       //log folder
│  ├─assets     //assets folderr
│  ├─myapp      //executable file
│  └─daemon    //daemon file compiled and copy from this package
```

The service name is explicit and independent from the executable filename. It may contain `A-Z`, `a-z`, `0-9`, `.`, `_`, `@`, and `-`. A relative executable name is resolved beside the daemon binary. An executable in another folder can be installed using its absolute path.


### Linux and FreeBSD

```sh
cd ./{your-project-folder}

sudo ./daemon install my-service myapp [arg1] [arg2] ...
sudo ./daemon install --stop-timeout 45s my-service myapp [arg1] [arg2] ...
sudo ./daemon install my-service myapp --port 8080 --config "configs/my app.toml"
sudo ./daemon install my-service "/opt/My App/myapp" --port 8080 --config "/opt/My App/config.toml"
sudo ./daemon list
sudo ./daemon ls
sudo ./daemon start my-service
sudo ./daemon status my-service
sudo ./daemon restart my-service
sudo ./daemon stop my-service
sudo ./daemon remove my-service
```

### macOS

Do not use `sudo`; it would create the per-user LaunchAgent for the root user instead of the logged-in user.

```sh
cd ./{your-project-folder}

./daemon install my-service myapp [arg1] [arg2] ...
./daemon install --stop-timeout 45s my-service myapp [arg1] [arg2] ...
./daemon list
./daemon start my-service
./daemon status my-service
./daemon restart my-service
./daemon stop my-service
./daemon remove my-service
```

### Windows PowerShell

Open PowerShell with **Run as administrator**, then run:

```powershell
cd C:\path\to\your-project-folder

.\daemon.exe install my-service .\myapp.exe [arg1] [arg2] ...
.\daemon.exe list
.\daemon.exe start my-service
.\daemon.exe status my-service
.\daemon.exe restart my-service
.\daemon.exe stop my-service
.\daemon.exe stop --stop-timeout 45s my-service
.\daemon.exe remove my-service
```

`--stop-timeout` defaults to `600s` and accepts positive, whole-second Go duration values such as `45s` or `10m`. On Unix platforms, set it during `install` so the generated service configuration waits up to that duration before forcing termination. The option must appear before the executable because arguments after the executable belong to the application.

On Windows, `--stop-timeout` on `stop`, `restart`, or `remove` controls only how long this tool waits for SCM to report `STOPPED`. A timeout returns an error and does not terminate the service process.

`list` and `ls` show each managed service and its current status:

```text
NAME        STATUS
api         stopped
my-service  running
```


### in the test folder there are test apps for different architecture

#### example you can run on arm64 op-system
```
sudo ./daemon install app-test app_test-linux-arm64
```
#### or run on arm32(armv6,armv7,etc..) op-system
```
sudo ./daemon install app-test app_test-linux-arm32
```





