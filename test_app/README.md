# Test HTTP Application

This configurable HTTP server verifies that daemon-util preserves application
arguments and controls a real process.

## Options

| Option | Type | Default |
| --- | --- | --- |
| `--enabled` | Boolean | `false` |
| `--message` | String | `hello from test app` |
| `--count` | Integer | `1` |
| `--port` | Integer | `18080` |
| `--file-path` | String | Empty (disabled) |
| `--stop-after` | Duration | `0` (disabled) |
| `--stop_delay` | Duration | `0` (disabled) |
| `--event-path` | String | Empty (disabled) |
| `--spawn-child` | Boolean | `false` |
| `--child-pid-path` | String | `child.pid` |

Use `--enabled=true` or `--enabled=false` for the Boolean option. Quote string
values that contain spaces. Durations use Go syntax, such as `30s`, `2m`, or
`1m30s`.

When `--file-path` is set, the application reads the file during startup and
returns its contents as `file_content` from the root endpoint. A missing or
unreadable file causes startup to fail.

When `--event-path` is set, the application appends JSON Lines records for
the lifecycle events `started`, `signal`, `stopped`, and `failure`.
Use `--spawn-child=true` to create a long-running child process and write its
process ID to `--child-pid-path`. These options are intended for service-manager
integration tests that verify graceful shutdown and, where the backend promises
it, descendant cleanup.

The parent test application does not stop the spawned child itself. This makes
the child a containment probe: tests require it to disappear only for backends
whose documented contract includes descendant cleanup. See the
[process-cleanup table](../readme.md#process-cleanup) for those guarantees.

The sample app's `--stop-after` and `--stop_delay` options are distinct and are
not aliases. `--stop-after` triggers an application failure; `--stop_delay`
delays graceful shutdown. The daemon CLI's separate `--stop-timeout` install
option controls when the selected backend escalates a stop request.

## Verify a relative file path

`./test_app/build.sh` creates `relative-path-test.txt` beside the generated
test-app binaries. Install a test app with that relative filename:

```sh
daemon_bin="$PWD/build/daemon-darwin-arm64"
app_bin="$PWD/test_app/build/test-app-darwin-arm64"

"$daemon_bin" install testapp "$app_bin" \
  --port 18080 \
  --file-path relative-path-test.txt
"$daemon_bin" start testapp
curl http://127.0.0.1:18080/
```

The response should contain:

```json
{
  "config": {
    "file_path": "relative-path-test.txt"
  },
  "file_content": "daemon-util relative path test passed\n"
}
```

This confirms that daemon-util starts the application with its working
directory set to the executable's directory, allowing relative application
paths to resolve beside the binary.
Stop and remove the service after testing:

```sh
"$daemon_bin" stop testapp
"$daemon_bin" remove testapp
```

## Verify automatic restart

Add `--stop-after 30s` when installing the service. When the timer expires, the
app shuts down and returns an error that `log.Fatal` reports with exit status 1,
allowing a service manager configured for automatic restart to launch it again:

```sh
daemon_bin="$PWD/build/daemon-darwin-arm64"
app_bin="$PWD/test_app/build/test-app-darwin-arm64"

"$daemon_bin" install testapp "$app_bin" --port 18080 --stop-after 30s
"$daemon_bin" start testapp
curl http://127.0.0.1:18080/
```

Query the endpoint once before the timeout and again after the service manager's restart delay. A new `pid` and `started_at` confirm that the process restarted. The timer applies after every launch, so the process continues cycling until the service is stopped or reinstalled without `--stop-after`.

The process writes its start time in RFC3339Nano format through the standard Go
logger whenever it launches. On backends that capture standard error, this
appears in the service log.

To test graceful-stop timeout handling, install the app with `--stop_delay`. It
waits for that duration after the manager sends `SIGTERM` on Unix or the Windows
wrapper delivers `CTRL_BREAK_EVENT` to its console process group:

```sh
"$daemon_bin" install --stop-timeout 10s testapp "$app_bin" --port 18080 --stop_delay 30s
```

With these values, the service manager or Windows wrapper should force
termination after 10 seconds. Use a delay shorter than `--stop-timeout` to test
successful graceful shutdown. The `--stop-after` failure timer does not apply
`--stop_delay`.

## Run directly

```sh
go run ./test_app \
  --enabled=true \
  --message "hello service" \
  --count 7 \
  --port 18080
```

Inspect it from another terminal:

```sh
curl http://127.0.0.1:18080/
curl http://127.0.0.1:18080/healthz
```

The root endpoint returns the parsed configuration, original argument list,
executable path, loaded file content, process ID, start time, and current time
as JSON.

## Build all platforms

Build the daemon and test app matrices:

```sh
./build.sh
./test_app/build.sh
```

Both scripts produce Darwin, FreeBSD, Linux, and Windows binaries for the
supported architectures. Linux additionally includes ARM32 and soft-float MIPS
and MIPSLE builds.

## Test with daemon on Linux or FreeBSD

Choose binaries matching the host architecture. For Linux AMD64:

```sh
daemon_bin="$PWD/build/daemon-linux-amd64"
app_bin="$PWD/test_app/build/test-app-linux-amd64"

sudo "$daemon_bin" install testapp "$app_bin" \
  --enabled=true \
  --message "hello service" \
  --count 7 \
  --port 18080

sudo "$daemon_bin" start testapp
sudo "$daemon_bin" status testapp
curl http://127.0.0.1:18080/
sudo "$daemon_bin" stop testapp
sudo "$daemon_bin" remove testapp
```

For FreeBSD, substitute the matching `daemon-freebsd-*` and
`test-app-freebsd-*` filenames.
Service installation and management on Linux and FreeBSD require root; the
examples therefore use `sudo`.

## Test with daemon on macOS

macOS installs a per-user launch agent, so do not use `sudo`.

For Apple Silicon:

```sh
daemon_bin="$PWD/build/daemon-darwin-arm64"
app_bin="$PWD/test_app/build/test-app-darwin-arm64"

"$daemon_bin" install testapp "$app_bin" \
  --enabled=true \
  --message "hello service" \
  --count 7 \
  --port 18080

"$daemon_bin" start testapp
curl http://127.0.0.1:18080/
"$daemon_bin" stop testapp
"$daemon_bin" remove testapp
```

Use the AMD64 binaries on Intel Macs.

## Test with daemon on Windows

Run PowerShell as Administrator, then use the binaries matching the host
architecture. The daemon wrapper hosts the console application as a Windows
service:

```powershell
$daemon = "$PWD\build\daemon-windows-amd64.exe"
$app = "$PWD\test_app\build\test-app-windows-amd64.exe"

& $daemon install testapp $app --enabled=true --message "hello service" --count 7 --port 18080
& $daemon start testapp
Invoke-RestMethod http://127.0.0.1:18080/
& $daemon stop testapp
& $daemon remove testapp
```