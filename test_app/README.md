# Test HTTP Application

This configurable HTTP server verifies that `daemon` preserves application arguments and controls a real process.

## Options

| Option | Type | Default |
| --- | --- | --- |
| `--enabled` | Boolean | `false` |
| `--message` | String | `hello from test app` |
| `--count` | Integer | `1` |
| `--port` | Integer | `18080` |
| `--stop-after` | Duration | `0` (disabled) |
| `--stop_delay` | Duration | `0` (disabled) |

Use `--enabled=true` or `--enabled=false` for the Boolean option. Quote string values that contain spaces. Durations use Go syntax, such as `30s`, `2m`, or `1m30s`.

## Verify automatic restart

Add `--stop-after 30s` when installing the service. The app shuts down and exits with status 1 after 30 seconds, allowing a service manager configured for automatic restart to launch it again:

```sh
daemon_bin="$PWD/build/daemon-darwin-arm64"
app_bin="$PWD/test_app/build/test-app-darwin-arm64"

"$daemon_bin" install test-app "$app_bin" --port 18080 --stop-after 30s
"$daemon_bin" start test-app
curl http://127.0.0.1:18080/
```

Query the endpoint once before the timeout and again after the service manager's restart delay. A new `pid` and `started_at` confirm that the process restarted. The timer applies after every launch, so the process continues cycling until the service is stopped or reinstalled without `--stop-after`.

The application also writes its start time to the service log in RFC3339 format whenever it launches.

To test graceful-stop timeout handling, install the app with `--stop_delay`. When it receives SIGTERM or the equivalent Windows service stop request, it waits for that duration before shutting down:

```sh
"$daemon_bin" install --stop-timeout 10s test-app "$app_bin" --port 18080 --stop_delay 30s
```

With these values, service managers that enforce `--stop-timeout` should force termination after 10 seconds. The `--stop-after` failure timer does not apply `--stop_delay`.

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

The root endpoint returns the parsed configuration, original argument list, executable path, process ID, start time, and current time as JSON.

## Build all platforms

Build the daemon and test app matrices:

```sh
./autobuild.sh
./test_app/build.sh
```

Both scripts produce Darwin, FreeBSD, Linux, and Windows binaries for the supported architectures.

## Test with daemon on Linux or FreeBSD

Choose binaries matching the host architecture. For Linux AMD64:

```sh
daemon_bin="$PWD/build/daemon-linux-amd64"
app_bin="$PWD/test_app/build/test-app-linux-amd64"

sudo "$daemon_bin" install test-app "$app_bin" \
  --enabled=true \
  --message "hello service" \
  --count 7 \
  --port 18080

sudo "$daemon_bin" start test-app
sudo "$daemon_bin" status test-app
curl http://127.0.0.1:18080/
sudo "$daemon_bin" stop test-app
sudo "$daemon_bin" remove test-app
```

For FreeBSD, substitute the matching `daemon-freebsd-*` and `test-app-freebsd-*` filenames.

## Test with daemon on macOS

For Apple Silicon:

```sh
daemon_bin="$PWD/build/daemon-darwin-arm64"
app_bin="$PWD/test_app/build/test-app-darwin-arm64"

"$daemon_bin" install test-app "$app_bin" \
  --enabled=true \
  --message "hello service" \
  --count 7 \
  --port 18080

"$daemon_bin" start test-app
curl http://127.0.0.1:18080/
"$daemon_bin" stop test-app
"$daemon_bin" remove test-app
```

Use the AMD64 binaries on Intel Macs.

## Test with daemon on Windows

Run PowerShell as Administrator, then use the binaries matching the host architecture:

```powershell
$daemon = "$PWD\build\daemon-windows-amd64.exe"
$app = "$PWD\test_app\build\test-app-windows-amd64.exe"

& $daemon install test-app $app --enabled=true --message "hello service" --count 7 --port 18080
& $daemon start test-app
Invoke-RestMethod http://127.0.0.1:18080/
& $daemon stop test-app
& $daemon remove test-app
```