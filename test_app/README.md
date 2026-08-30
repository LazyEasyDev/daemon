# Test HTTP Application

This configurable HTTP server verifies that `daemon` preserves application arguments and controls a real process.

## Options

| Option | Type | Default |
| --- | --- | --- |
| `--enabled` | Boolean | `false` |
| `--message` | String | `hello from test app` |
| `--count` | Integer | `1` |
| `--port` | Integer | `18080` |

Use `--enabled=true` or `--enabled=false` for the Boolean option. Quote string values that contain spaces.

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