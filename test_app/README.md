# Test HTTP Application

This configurable HTTP server verifies that `daemon` preserves application arguments and controls a real process.

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

Use `--enabled=true` or `--enabled=false` for the Boolean option. Quote string values that contain spaces. Durations use Go syntax, such as `30s`, `2m`, or `1m30s`.

When `--file-path` is set, the application reads the file during startup and
returns its contents as `file_content` from the root endpoint. A missing or
unreadable file causes startup to fail.

When `--event-path` is set, the application appends JSON Lines records for
startup, received stop signals, graceful completion, and configured failures.
Use `--spawn-child=true` to create a long-running child process and write its
process ID to `--child-pid-path`. These options are intended for service-manager
integration tests that verify graceful shutdown and descendant cleanup.

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

This confirms that daemon-util starts the application from the executable's
directory, allowing relative application paths to resolve beside the binary.
Stop and remove the service after testing:

```sh
"$daemon_bin" stop testapp
"$daemon_bin" remove testapp
```

## Verify automatic restart

Add `--stop-after 30s` when installing the service. The app shuts down and exits with status 1 after 30 seconds, allowing a service manager configured for automatic restart to launch it again:

```sh
daemon_bin="$PWD/build/daemon-darwin-arm64"
app_bin="$PWD/test_app/build/test-app-darwin-arm64"

"$daemon_bin" install testapp "$app_bin" --port 18080 --stop-after 30s
"$daemon_bin" start testapp
curl http://127.0.0.1:18080/
```

Query the endpoint once before the timeout and again after the service manager's restart delay. A new `pid` and `started_at` confirm that the process restarted. The timer applies after every launch, so the process continues cycling until the service is stopped or reinstalled without `--stop-after`.

The application also writes its start time to the service log in RFC3339 format whenever it launches.

To test graceful-stop timeout handling, install the app with `--stop_delay`. It waits for that duration after receiving SIGTERM on Unix or `CTRL_BREAK_EVENT` on Windows:

```sh
"$daemon_bin" install --stop-timeout 10s testapp "$app_bin" --port 18080 --stop_delay 30s
```

With these values, the service manager or Windows wrapper should force termination after 10 seconds. Use a delay shorter than `--stop-timeout` to test successful graceful shutdown. The `--stop-after` failure timer does not apply `--stop_delay`.

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

Both scripts produce Darwin, FreeBSD, Linux, and Windows binaries for the supported architectures.

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

For FreeBSD, substitute the matching `daemon-freebsd-*` and `test-app-freebsd-*` filenames.

## Test with daemon on macOS

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

Run PowerShell as Administrator, then use the binaries matching the host architecture. The application is hosted by the daemon wrapper:

```powershell
$daemon = "$PWD\build\daemon-windows-amd64.exe"
$app = "$PWD\test_app\build\test-app-windows-amd64.exe"

& $daemon install testapp $app --enabled=true --message "hello service" --count 7 --port 18080
& $daemon start testapp
Invoke-RestMethod http://127.0.0.1:18080/
& $daemon stop testapp
& $daemon remove testapp
```